package access

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	traceprivatev1 "github.com/talgent-work/contracts/gen/go/trace/private/v1"

	"google.golang.org/protobuf/proto"

	accountprivatev1 "github.com/talgent-work/contracts/gen/go/account/private/v1"
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
			cfg := Config{WorkCallerAccessAddresses: map[string]string{"team": server.URL}, AgentAddress: server.URL, AgentControlToken: "token"}
			callers, err := NewWorkCallerAccess(server.Client(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			works, err := NewAgent(server.Client(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			for name, err := range map[string]error{
				"work caller": callers.AuthorizeWorkCallerReader(context.Background(), "reader", query.WorkCaller{Kind: "team", ID: "team-1"}),
				"work":        works.AuthorizeWorkReader(context.Background(), "reader", "work-1"),
			} {
				if !errors.Is(err, tc.want) {
					t.Fatalf("%s: got %v, want %v", name, err, tc.want)
				}
			}
		})
	}
}

// The Work Caller's kind selects the extension asked, on the reader's behalf
// through the trusted delegation headers; a kind no extension decides here
// admits no one, and nothing is asked.
func TestWorkCallerKindSelectsTheExtensionAsked(t *testing.T) {
	asked := map[string]string{}
	extension := func(name string) *httptest.Server {
		return respond(t, http.StatusOK, "", &traceprivatev1.DecideWorkCallerAccessResponse{}, func(r *http.Request) {
			if r.Header.Get(authn.HeaderServiceID) != "trace" || r.Header.Get(authn.HeaderDelegatedUserID) != "reader" {
				t.Error("missing trusted delegation")
			}
			asked[name] = r.URL.Path
		})
	}
	teams, boards := extension("teams"), extension("boards")
	callers, err := NewWorkCallerAccess(teams.Client(), Config{WorkCallerAccessAddresses: map[string]string{"team": teams.URL, "board": boards.URL + "/"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := callers.AuthorizeWorkCallerReader(context.Background(), "reader", query.WorkCaller{Kind: "board", ID: "board-1"}); err != nil {
		t.Fatalf("admitted member refused: %v", err)
	}
	if _, ok := asked["teams"]; ok || asked["boards"] != "/trace.private.v1.WorkCallerAccessService/DecideWorkCallerAccess" {
		t.Fatalf("asked = %v, want only the board extension's decision", asked)
	}
	if err := callers.AuthorizeWorkCallerReader(context.Background(), "reader", query.WorkCaller{Kind: "ledger", ID: "ledger-1"}); !errors.Is(err, query.ErrAuthorizationPermissionDenied) {
		t.Fatalf("undecided kind: %v", err)
	}
	if len(asked) != 1 {
		t.Fatalf("asked = %v after an undecided kind", asked)
	}
}

// Agent attributes Works to an Application with the control token, and an
// outage is never mistaken for an answer.
func TestAgentAttributesWorksWithTheControlToken(t *testing.T) {
	server := respond(t, http.StatusOK, "", &agentprivatev1.ListWorkAttributionsResponse{Works: []*agentprivatev1.WorkAttribution{{WorkId: "work-1", Labels: map[string]string{"tenant": "acme"}}}}, func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing control token")
		}
	})
	agent, err := NewAgent(server.Client(), Config{AgentAddress: server.URL, AgentControlToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	works, err := agent.ListWorkAttributions(context.Background(), "application-1", []string{"work-1", "work-2"})
	if err != nil || len(works) != 1 || works[0].WorkID != "work-1" || works[0].Labels["tenant"] != "acme" {
		t.Fatalf("attributions = %#v, %v", works, err)
	}
	down := respond(t, http.StatusServiceUnavailable, `{"code":"unavailable"}`, nil, func(*http.Request) {})
	agent, _ = NewAgent(down.Client(), Config{AgentAddress: down.URL, AgentControlToken: "token"})
	if _, err := agent.ListWorkAttributions(context.Background(), "application-1", []string{"work-1"}); !errors.Is(err, query.ErrAuthorizationUnavailable) {
		t.Fatalf("outage: %v", err)
	}
}

// Account's Application Ownership Query is asked as Observability; an
// Application Account never registered is unknown, not an outage.
func TestAccountAnswersApplicationOwnership(t *testing.T) {
	server := respond(t, http.StatusOK, "", &accountprivatev1.GetApplicationResponse{Application: &accountprivatev1.OwnedApplication{ApplicationId: "application-1", OwnerUserId: "owner-1", Removed: true}}, func(r *http.Request) {
		if r.Header.Get(authn.HeaderServiceID) != "trace" {
			t.Error("missing service identity")
		}
	})
	accounts, err := NewAccount(server.Client(), Config{AccountAddress: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := accounts.GetApplication(context.Background(), "application-1")
	if err != nil || owned.OwnerUserID != "owner-1" || !owned.Removed {
		t.Fatalf("owned = %#v, %v", owned, err)
	}
	unknown := respond(t, http.StatusNotFound, `{"code":"not_found"}`, nil, func(*http.Request) {})
	accounts, _ = NewAccount(unknown.Client(), Config{AccountAddress: unknown.URL})
	if _, err := accounts.GetApplication(context.Background(), "application-2"); !errors.Is(err, query.ErrApplicationUnknown) {
		t.Fatalf("unknown: %v", err)
	}
}

// Agent's Work access admits only the owner, Work Share holders and the
// Application Owner; a public Work's other readers are a Work Caller's
// members, whom only the extension admits.
func TestWorkAccessAdmitsOwnerAndShareHoldersOnly(t *testing.T) {
	for basis, admitted := range map[agentprivatev1.WorkReadBasis]bool{
		agentprivatev1.WorkReadBasis_WORK_READ_BASIS_OWNER:             true,
		agentprivatev1.WorkReadBasis_WORK_READ_BASIS_SHARE:             true,
		agentprivatev1.WorkReadBasis_WORK_READ_BASIS_APPLICATION_OWNER: true,
		agentprivatev1.WorkReadBasis_WORK_READ_BASIS_PUBLIC:            false,
		agentprivatev1.WorkReadBasis_WORK_READ_BASIS_UNSPECIFIED:       false,
	} {
		t.Run(basis.String(), func(t *testing.T) {
			server := respond(t, http.StatusOK, "", &agentprivatev1.AdmitWorkReaderResponse{Basis: basis}, func(r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing control token")
				}
			})
			works, err := NewAgent(server.Client(), Config{AgentAddress: server.URL, AgentControlToken: "token"})
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
	server := respond(t, http.StatusOK, "", &traceprivatev1.DecideWorkCallerAccessResponse{}, func(*http.Request) { t.Error("authority asked") })
	cfg := Config{WorkCallerAccessAddresses: map[string]string{"team": server.URL}, AgentAddress: server.URL, AgentControlToken: "token"}
	callers, _ := NewWorkCallerAccess(server.Client(), cfg)
	works, _ := NewAgent(server.Client(), cfg)
	if err := callers.AuthorizeWorkCallerReader(context.Background(), "", query.WorkCaller{Kind: "team", ID: "team-1"}); !errors.Is(err, query.ErrAuthorizationUnauthenticated) {
		t.Fatalf("anonymous: %v", err)
	}
	if err := works.AuthorizeWorkReader(context.Background(), "reader", " "); !errors.Is(err, query.ErrAuthorizationPermissionDenied) {
		t.Fatalf("no Work: %v", err)
	}
}

// A deployment without extensions configures no Work Caller access
// decision: Standalone Works stay readable under Agent's Work access, and no
// extension kind is read. A configured kind must be one an extension defines,
// at an absolute http(s) address.
func TestWorkCallerAccessConfiguration(t *testing.T) {
	base := Config{AgentAddress: "http://controlplane:8080", AgentControlToken: "token", AccountAddress: "http://controlplane:8080"}
	if err := base.Validate(); err != nil {
		t.Fatalf("configuration without extensions refused: %v", err)
	}
	callers, err := NewWorkCallerAccess(http.DefaultClient, base)
	if err != nil {
		t.Fatal(err)
	}
	if err := callers.AuthorizeWorkCallerReader(context.Background(), "reader", query.WorkCaller{Kind: "team", ID: "team-1"}); !errors.Is(err, query.ErrAuthorizationPermissionDenied) {
		t.Fatalf("no extension configured: %v", err)
	}
	for name, addresses := range map[string]map[string]string{
		"relative address":  {"team": "extension:8080"},
		"core person kind":  {"person": "http://extension:8080"},
		"core application":  {"application": "http://extension:8080"},
		"malformed kind":    {"Team": "http://extension:8080"},
		"kind with a colon": {"team:x": "http://extension:8080"},
	} {
		cfg := base
		cfg.WorkCallerAccessAddresses = addresses
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
