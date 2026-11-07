package nativeapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/nativeapi"
)

// verifier accepts the Work's credential; interactive reports whether the
// Work has reached its first turn, which only business actions require.
type verifier struct{ interactive bool }

func (v verifier) VerifyNativeProcess(_ context.Context, workID, credential string) (authn.NativeProcess, error) {
	if workID == "work" && credential == "secret" && v.interactive {
		return authn.NativeProcess{WorkID: "work", SubjectID: "owner", ProjectID: "project-a", WorkSubjectKind: "topic", WorkSubjectID: "topic-1", Purpose: "work"}, nil
	}
	return authn.NativeProcess{}, errors.New("not admitted")
}

// The procedures a product context lists; the native API routes exactly
// what it is given, whoever owns it.
var (
	reads = []string{"/talgent.test.v1.ReadService/Get", "/talgent.test.v1.ReadService/Search"}
	calls = nativeapi.Calls{
		Procedures: []string{"/talgent.test.v1.CommandService/Change", "/talgent.test.v1.CommandService/Record"},
		SpaceTokenProcedures: []string{
			storageprivatev1connect.ArtifactServiceCreateArtifactProcedure, storageprivatev1connect.ArtifactServiceReplaceArtifactProcedure,
			storageprivatev1connect.ArtifactServiceDeleteArtifactProcedure,
		},
	}
)

// handler routes to a controlplane that records the Project it was given.
func handler(t *testing.T) (http.Handler, *[]string) {
	t.Helper()
	var projects []string
	controlplane := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		process, _ := authn.NativeProcessFromContext(r.Context())
		projects = append(projects, process.ProjectID)
		w.WriteHeader(http.StatusOK)
	})
	// The Work is still starting: business actions are refused, reads are not.
	h, err := nativeapi.NewHandler(controlplane, gatewayToken, verifier{interactive: false}, calls,
		nativeapi.ReadProcedures{Procedures: reads, Verifier: verifier{interactive: true}})
	require.NoError(t, err)
	return h, &projects
}

func post(h http.Handler, procedure string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, procedure, strings.NewReader(`{"project_id":"project-b"}`))
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	return recorder
}

const gatewayToken = "gateway-secret"

// viaGateway adds the Gateway's token to a request's headers.
func viaGateway(headers map[string]string) map[string]string {
	out := map[string]string{authn.HeaderGatewayToken: gatewayToken}
	for key, value := range headers {
		out[key] = value
	}
	return out
}

var work = map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "secret", authn.HeaderGatewayToken: gatewayToken}

// Agents reach the read procedures as soon as their sandbox is live, before
// the first turn, always for the Project of their verified Work; a Project
// named in the request body is never consulted. Business actions still wait
// for an interactive Work.
func TestReadsServeALiveWorkBeforeItsFirstTurn(t *testing.T) {
	h, projects := handler(t)
	for _, procedure := range reads {
		require.Equal(t, http.StatusOK, post(h, procedure, work).Code, procedure)
	}
	require.Equal(t, []string{"project-a", "project-a"}, *projects)
	require.Equal(t, http.StatusUnauthorized, post(h, calls.Procedures[0], work).Code, "an action-bearing procedure keeps the interaction gate")
}

// Nothing unlisted is reachable from a sandbox, not even a listed service's
// prefix, whatever credential it presents.
func TestUnlistedProceduresAreUnreachable(t *testing.T) {
	h, projects := handler(t)
	for _, procedure := range []string{"/talgent.test.v1.CommandService/Configure", "/talgent.test.v1.ReadService/"} {
		require.Equal(t, http.StatusNotFound, post(h, procedure, work).Code, procedure)
	}
	require.Empty(t, *projects)
}

// A forged credential, or a trusted identity header smuggled beside a real
// one, is rejected before the business handler sees the request.
func TestForgedIdentityIsRejected(t *testing.T) {
	h, projects := handler(t)
	procedure := reads[0]
	require.Equal(t, http.StatusUnauthorized, post(h, procedure, viaGateway(map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "guess"})).Code)
	require.Equal(t, http.StatusUnauthorized, post(h, procedure, viaGateway(map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "secret", "X-Talgent-Project-Id": "project-b"})).Code)
	require.Equal(t, http.StatusUnauthorized, post(h, procedure, viaGateway(nil)).Code)
	require.Empty(t, *projects)
}

// Every listed call reaches its owner for an interactive Work; a missing
// route would fail the tool with 404.
func TestEveryListedCallReachesItsOwner(t *testing.T) {
	var reached []string
	controlplane := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})
	h, err := nativeapi.NewHandler(controlplane, gatewayToken, verifier{interactive: true}, calls, nativeapi.ReadProcedures{Verifier: verifier{interactive: true}})
	require.NoError(t, err)
	for _, procedure := range calls.Procedures {
		require.Equal(t, http.StatusOK, post(h, procedure, work).Code, procedure)
	}
	require.Equal(t, calls.Procedures, reached)
}

// A Work's Artifact commands reach Storage with its Space Token; any
// other procedure presenting a Space Token is refused.
func TestSpaceTokensTravelOnlyWithArtifactCommands(t *testing.T) {
	var tokens []string
	controlplane := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get(authn.HeaderSpaceToken))
		w.WriteHeader(http.StatusOK)
	})
	h, err := nativeapi.NewHandler(controlplane, gatewayToken, verifier{interactive: true}, calls, nativeapi.ReadProcedures{Verifier: verifier{interactive: true}})
	require.NoError(t, err)
	withToken := map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "secret", authn.HeaderGatewayToken: gatewayToken, authn.HeaderSpaceToken: "space"}
	for _, procedure := range []string{
		storageprivatev1connect.ArtifactServiceCreateArtifactProcedure,
		storageprivatev1connect.ArtifactServiceReplaceArtifactProcedure,
		storageprivatev1connect.ArtifactServiceDeleteArtifactProcedure,
	} {
		require.Equal(t, http.StatusOK, post(h, procedure, withToken).Code, procedure)
	}
	require.Equal(t, []string{"space", "space", "space"}, tokens)
	require.Equal(t, http.StatusUnauthorized, post(h, calls.Procedures[0], withToken).Code)
}

// A Work downloads an Access Link it was issued, still only with its own
// current credential; no other GET is reachable.
func TestWorksDownloadAccessLinksWithTheirCredential(t *testing.T) {
	var queries []string
	controlplane := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		w.WriteHeader(http.StatusOK)
	})
	h, err := nativeapi.NewHandler(controlplane, gatewayToken, verifier{interactive: true}, calls, nativeapi.ReadProcedures{Verifier: verifier{interactive: true}})
	require.NoError(t, err)
	get := func(target string, headers map[string]string) int {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, request)
		return recorder.Code
	}
	require.Equal(t, http.StatusOK, get(nativeapi.AccessLinkPath+"?grant=signed", work))
	require.Equal(t, []string{"grant=signed"}, queries)
	require.Equal(t, http.StatusUnauthorized, get(nativeapi.AccessLinkPath+"?grant=signed", viaGateway(nil)))
	require.Equal(t, http.StatusNotFound, get("/v1/storage/shares/token", work))
	require.Equal(t, http.StatusForbidden, get(calls.Procedures[0], work), "procedures stay POST only")
}

// Only the Gateway reaches a native API that names its token: a valid Work
// credential without the Gateway's token, or with a wrong one, is refused
// before any procedure, and the token never travels on to the procedure.
func TestOnlyTheGatewayReachesTheNativeAPI(t *testing.T) {
	var forwarded []string
	controlplane := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = append(forwarded, r.Header.Get(authn.HeaderGatewayToken))
		w.WriteHeader(http.StatusOK)
	})
	h, err := nativeapi.NewHandler(controlplane, gatewayToken, verifier{interactive: true}, calls, nativeapi.ReadProcedures{Verifier: verifier{interactive: true}})
	require.NoError(t, err)
	direct := map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "secret"}
	require.Equal(t, http.StatusForbidden, post(h, calls.Procedures[0], direct).Code)
	direct[authn.HeaderGatewayToken] = "guess"
	require.Equal(t, http.StatusForbidden, post(h, calls.Procedures[0], direct).Code)
	require.Empty(t, forwarded)
	require.Equal(t, http.StatusOK, post(h, calls.Procedures[0], work).Code)
	require.Equal(t, []string{""}, forwarded)
}

// A native API that names no Gateway token admits a Work's Agent calling it
// directly with its Work credential, under the same rules as a call through
// the Gateway: its procedures, its Space Token only on Artifact commands, a
// current credential for actions and a live one for reads. The sandbox's
// Credential Proxy adds the Space Token to every request it sends there, so
// any other procedure, and a download, receives the call without it. A
// Gateway token presented beside the credential adds nothing and never
// travels on.
func TestAWorkReachesANativeAPIWithoutAGatewayDirectly(t *testing.T) {
	type reached struct{ path, project, gateway, space string }
	var forwarded []reached
	controlplane := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		process, _ := authn.NativeProcessFromContext(r.Context())
		forwarded = append(forwarded, reached{r.URL.Path, process.ProjectID, r.Header.Get(authn.HeaderGatewayToken), r.Header.Get(authn.HeaderSpaceToken)})
		w.WriteHeader(http.StatusOK)
	})
	h, err := nativeapi.NewHandler(controlplane, "", verifier{interactive: false}, calls, nativeapi.ReadProcedures{Procedures: reads, Verifier: verifier{interactive: true}})
	require.NoError(t, err)
	direct := map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "secret"}
	require.Equal(t, http.StatusOK, post(h, reads[0], direct).Code)
	require.Equal(t, http.StatusOK, post(h, reads[1], viaGateway(direct)).Code)
	require.Equal(t, []reached{{reads[0], "project-a", "", ""}, {reads[1], "project-a", "", ""}}, forwarded)
	require.Equal(t, http.StatusUnauthorized, post(h, calls.Procedures[0], direct).Code, "an action still needs an interactive Work")
	require.Equal(t, http.StatusUnauthorized, post(h, reads[0], map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "guess"}).Code)
	require.Equal(t, http.StatusUnauthorized, post(h, reads[0], map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "secret", "X-Talgent-Project-Id": "project-b"}).Code)
	require.Equal(t, http.StatusUnauthorized, post(h, reads[0], nil).Code)
	require.Equal(t, http.StatusNotFound, post(h, "/talgent.test.v1.CommandService/Configure", direct).Code)
	require.Len(t, forwarded, 2)

	forwarded = nil
	interactive, err := nativeapi.NewHandler(controlplane, "", verifier{interactive: true}, calls, nativeapi.ReadProcedures{Verifier: verifier{interactive: true}})
	require.NoError(t, err)
	withToken := map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "secret", authn.HeaderSpaceToken: "space"}
	require.Equal(t, http.StatusOK, post(interactive, storageprivatev1connect.ArtifactServiceCreateArtifactProcedure, withToken).Code)
	require.Equal(t, http.StatusOK, post(interactive, calls.Procedures[0], withToken).Code)
	request := httptest.NewRequest(http.MethodGet, nativeapi.AccessLinkPath+"?grant=signed", nil)
	for key, value := range withToken {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	interactive.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []reached{
		{storageprivatev1connect.ArtifactServiceCreateArtifactProcedure, "project-a", "", "space"},
		{calls.Procedures[0], "project-a", "", ""},
		{nativeapi.AccessLinkPath, "project-a", "", ""},
	}, forwarded, "a Space Token travels only with Artifact commands")
}

// A Managed Work calls the native API through the public route, whose Envoy
// adds headers of its own: they carry no identity and are dropped unread,
// while a trusted identity header the caller adds is still refused.
func TestAManagedWorkReachesTheNativeAPIThroughThePublicRoute(t *testing.T) {
	var envoy []string
	controlplane := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envoy = append(envoy, r.Header.Get("X-Envoy-External-Address"))
		w.WriteHeader(http.StatusOK)
	})
	h, err := nativeapi.NewHandler(controlplane, "", verifier{interactive: true}, calls, nativeapi.ReadProcedures{Procedures: reads, Verifier: verifier{interactive: true}})
	require.NoError(t, err)
	routed := map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "secret", "X-Envoy-External-Address": "198.51.100.7", "X-Envoy-Expected-Rq-Timeout-Ms": "300000"}
	require.Equal(t, http.StatusOK, post(h, reads[0], routed).Code)
	require.Equal(t, []string{""}, envoy, "Envoy's headers never reach the product")
	routed["X-Talgent-Subject"] = "someone-else"
	require.Equal(t, http.StatusUnauthorized, post(h, reads[0], routed).Code)
	require.Len(t, envoy, 1)
}

// The Gateway learns whose Work a credential is as soon as the sandbox is
// live, before the Work's first turn, so it can list tools and give the
// session its start context; nothing else is served that early.
func TestTheGatewayLearnsTheLiveWorkBeforeItsFirstTurn(t *testing.T) {
	h, _ := handler(t)
	response := post(h, nativeapi.IdentityPath, work)
	require.Equal(t, http.StatusOK, response.Code)
	var identity nativeapi.Identity
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &identity))
	require.Equal(t, nativeapi.Identity{WorkID: "work", ProjectID: "project-a", SubjectKind: "topic", SubjectID: "topic-1", Purpose: "work"}, identity)
	require.Equal(t, http.StatusUnauthorized, post(h, nativeapi.IdentityPath, viaGateway(map[string]string{authn.HeaderNativeWorkID: "work", authn.HeaderNativeCredential: "guess"})).Code)
}
