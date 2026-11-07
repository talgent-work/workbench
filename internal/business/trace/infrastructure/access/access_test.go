package access

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	traceprivatev1 "github.com/talgent-work/contracts/gen/go/trace/private/v1"

	"google.golang.org/protobuf/proto"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// respond answers every call with one status, or with message on success.
func respond(t *testing.T, status int, body string, message proto.Message, inspect func(*http.Request)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inspect(r)
		if status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		payload, err := proto.Marshal(message)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/proto")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	return server
}

// An authority's refusal or outage never admits the reader; an outage stays
// distinguishable from a refusal.
func TestAuthorityFailuresNeverAdmit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"refused", 403, `{"code":"permission_denied","message":"private diagnostic"}`, query.ErrAuthorizationPermissionDenied},
		{"unknown target", 404, `{"code":"not_found"}`, query.ErrAuthorizationPermissionDenied},
		{"unauthenticated", 401, `{"code":"unauthenticated"}`, query.ErrAuthorizationUnauthenticated},
		{"unavailable", 503, `{"code":"unavailable"}`, query.ErrAuthorizationUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := respond(t, tc.status, tc.body, nil, func(*http.Request) {})
			cfg := Config{ProjectAccessAddress: server.URL, AgentAddress: server.URL, AgentControlToken: "token"}
			projects, err := NewProjectAccess(server.Client(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			works, err := NewWorkAccess(server.Client(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			for name, err := range map[string]error{
				"project": projects.AuthorizeProjectReader(context.Background(), "reader", "project-1"),
				"work":    works.AuthorizeWorkReader(context.Background(), "reader", "work-1"),
			} {
				if !errors.Is(err, tc.want) {
					t.Fatalf("%s: got %v, want %v", name, err, tc.want)
				}
			}
		})
	}
}

// The Project access decision is asked on the reader's behalf through the
// trusted delegation headers.
func TestProjectAccessDelegatesTheReader(t *testing.T) {
	server := respond(t, http.StatusOK, "", &traceprivatev1.DecideProjectAccessResponse{}, func(r *http.Request) {
		if r.Header.Get(authn.HeaderServiceID) != "trace" || r.Header.Get(authn.HeaderDelegatedUserID) != "reader" {
			t.Error("missing trusted delegation")
		}
	})
	projects, err := NewProjectAccess(server.Client(), Config{ProjectAccessAddress: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := projects.AuthorizeProjectReader(context.Background(), "reader", "project-1"); err != nil {
		t.Fatalf("admitted member refused: %v", err)
	}
}

// Agent's Work access admits only the owner and Work Share holders; a public
// Work's other readers need a Project, which a Work without one never has.
func TestWorkAccessAdmitsOwnerAndShareHoldersOnly(t *testing.T) {
	for basis, admitted := range map[agentprivatev1.WorkReadBasis]bool{
		agentprivatev1.WorkReadBasis_WORK_READ_BASIS_OWNER:       true,
		agentprivatev1.WorkReadBasis_WORK_READ_BASIS_SHARE:       true,
		agentprivatev1.WorkReadBasis_WORK_READ_BASIS_PUBLIC:      false,
		agentprivatev1.WorkReadBasis_WORK_READ_BASIS_UNSPECIFIED: false,
	} {
		t.Run(basis.String(), func(t *testing.T) {
			server := respond(t, http.StatusOK, "", &agentprivatev1.AdmitWorkReaderResponse{Basis: basis}, func(r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing control token")
				}
			})
			works, err := NewWorkAccess(server.Client(), Config{AgentAddress: server.URL, AgentControlToken: "token"})
			if err != nil {
				t.Fatal(err)
			}
			err = works.AuthorizeWorkReader(context.Background(), "reader", "work-1")
			if admitted != (err == nil) || (!admitted && !errors.Is(err, query.ErrAuthorizationPermissionDenied)) {
				t.Fatalf("basis %s: %v", basis, err)
			}
		})
	}
}

// Without a reader or a target nothing is asked and nothing is admitted.
func TestMissingReaderOrTargetIsRefusedLocally(t *testing.T) {
	server := respond(t, http.StatusOK, "", &traceprivatev1.DecideProjectAccessResponse{}, func(*http.Request) { t.Error("authority asked") })
	cfg := Config{ProjectAccessAddress: server.URL, AgentAddress: server.URL, AgentControlToken: "token"}
	projects, _ := NewProjectAccess(server.Client(), cfg)
	works, _ := NewWorkAccess(server.Client(), cfg)
	if err := projects.AuthorizeProjectReader(context.Background(), "", "project-1"); !errors.Is(err, query.ErrAuthorizationUnauthenticated) {
		t.Fatalf("anonymous: %v", err)
	}
	if err := works.AuthorizeWorkReader(context.Background(), "reader", " "); !errors.Is(err, query.ErrAuthorizationPermissionDenied) {
		t.Fatalf("no Work: %v", err)
	}
}

// A deployment without the Project access decision configures none: Works
// without a Project stay readable under Agent's Work access, and nothing
// answers for a Project.
func TestUnconfiguredProjectAccessProvidesNoDecision(t *testing.T) {
	cfg := Config{AgentAddress: "http://controlplane:8080", AgentControlToken: "token"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("configuration without the Project access decision refused: %v", err)
	}
	projects, err := NewProjectAccess(http.DefaultClient, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if projects != nil {
		t.Fatalf("unconfigured Project access decision = %#v, want none", projects)
	}
	if err := (Config{ProjectAccessAddress: "project-access:8080", AgentAddress: "http://controlplane:8080", AgentControlToken: "token"}).Validate(); err == nil {
		t.Fatal("a configured Project access address must be an absolute http(s) URL")
	}
}
