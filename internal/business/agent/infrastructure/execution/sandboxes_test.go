package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	shared "github.com/talgent-work/workbench/internal/pkg/opensandbox"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const sandboxID = "a4147254-fb69-4e47-b96b-57c0e3d76fcf"

// fixturePlatformAPIAddress is the native API every fixture sandbox reaches; it
// resolves through fixtureLookup without DNS.
const fixturePlatformAPIAddress = "http://controlplane.talgent.svc.cluster.local"

func init() {
	resolve := lookupHost
	lookupHost = func(ctx context.Context, host string) ([]netip.Addr, error) {
		if strings.HasSuffix(host, ".svc.cluster.local") {
			return []netip.Addr{netip.MustParseAddr("10.43.0.9")}, nil
		}
		return resolve(ctx, host)
	}
}

func config() SandboxConfig {
	return SandboxConfig{Models: fixtureModels(), Image: "native-claude:test", Environment: map[string]string{platformAPIAddressEnv: fixturePlatformAPIAddress}, Resources: corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
	}}
}
func request() domain.StartExecutionRequest {
	return domain.StartExecutionRequest{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, WorkID: "session", Capabilities: domain.CapabilitySnapshot{Prompt: "fixed prompt\n", Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, HarnessSessionReference: "context", ContextMode: domain.ContextContinue, WorkspaceReference: "workspace", HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}}
}

const fixtureAllocationID = "01900000-0000-7000-8000-000000000001"

func owner(r domain.StartExecutionRequest) Allocation {
	return Allocation{WorkID: r.WorkID, ID: fixtureAllocationID}
}

// submit allocates the fixture sandbox for the request's live allocation.
func (s *Sandboxes) submitRequest(ctx context.Context, r domain.StartExecutionRequest, token string) (string, error) {
	spec, err := s.specification(ctx, r, testMounts, owner(r), token, "work-credential-fixture")
	if err != nil {
		return "", err
	}
	return s.submit(ctx, owner(r), spec)
}

func (s *Sandboxes) lookupRequest(ctx context.Context, r domain.StartExecutionRequest, token string) (string, error) {
	spec, err := s.specification(ctx, r, testMounts, owner(r), token, "work-credential-fixture")
	if err != nil {
		return "", err
	}
	return s.lookup(ctx, owner(r), spec)
}

type lifecycleFixture struct {
	mu                 sync.Mutex
	loseCreateResponse bool
	create             sdk.CreateSandboxRequest
	info               *sdk.SandboxInfo
	pages              [][]sdk.SandboxInfo
	creates, deletes   int
	createFailure      bool
	refuseStatus       int
	createOnRefusal    bool
	endpoint           string
	egress             string
	egressToken        string
	vault              *vaultFixture
	inspectStatus      int
	endpointStatus     int
	// requests counts every call to the environment authority.
	requests int
}

func (f *lifecycleFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if r.Header.Get("OPEN-SANDBOX-API-KEY") != "lifecycle-secret" {
		w.WriteHeader(401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/sandboxes":
		f.creates++
		if err := json.NewDecoder(r.Body).Decode(&f.create); err != nil {
			w.WriteHeader(400)
			return
		}
		if f.refuseStatus != 0 {
			if f.createOnRefusal {
				f.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: f.create.Metadata, Status: sdk.SandboxStatus{State: sdk.StateRunning}}
			}
			http.Error(w, "invalid parameters", f.refuseStatus)
			return
		}
		if f.createFailure {
			http.Error(w, "must-not-leak-token", 500)
			return
		}
		f.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: f.create.Metadata, Status: sdk.SandboxStatus{State: sdk.StateRunning}}
		if f.loseCreateResponse {
			http.Error(w, "response lost after allocation", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(f.info)
	case r.Method == "GET" && r.URL.Path == "/v1/sandboxes":
		page := 0
		if r.URL.Query().Get("page") == "2" {
			page = 1
		}
		var items []sdk.SandboxInfo
		if len(f.pages) > page {
			items = f.pages[page]
		} else if f.info != nil {
			items = []sdk.SandboxInfo{*f.info}
		}
		_ = json.NewEncoder(w).Encode(sdk.ListSandboxesResponse{Items: items, Pagination: sdk.PaginationInfo{HasNextPage: page+1 < len(f.pages)}})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, fmt.Sprintf("/endpoints/%d", CredentialManagementPort)):
		if f.info == nil {
			// A deleted sandbox exposes no endpoints.
			w.WriteHeader(404)
			return
		}
		token := f.egressToken
		if token == "" {
			token = "egress-secret"
		}
		_ = json.NewEncoder(w).Encode(sdk.Endpoint{Endpoint: f.egress, Headers: map[string]string{"OPENSANDBOX-EGRESS-AUTH": token}})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/endpoints/44772"):
		if f.endpointStatus != 0 {
			w.WriteHeader(f.endpointStatus)
			return
		}
		if r.URL.Query().Get("use_server_proxy") != "false" {
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(sdk.Endpoint{Endpoint: f.endpoint})
	case r.URL.Path == "/v1/sandboxes/"+sandboxID:
		if r.Method == "GET" && f.inspectStatus != 0 {
			w.WriteHeader(f.inspectStatus)
			return
		}
		if f.info == nil {
			w.WriteHeader(404)
			return
		}
		if r.Method == "DELETE" {
			f.deletes++
			f.info = nil
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(f.info)
	default:
		w.WriteHeader(404)
	}
}

// testMounts are the Mount Grants Storage issues for request's spaces.
var testMounts = domain.SandboxMounts{Workspace: &domain.MountGrant{ClaimName: "nas-root", SubPath: "workspaces/key"}, Home: domain.MountGrant{ClaimName: "nas-root", SubPath: "homes/key"}}

func sandboxClient(t *testing.T) (*Sandboxes, *fake.Clientset, *lifecycleFixture) {
	t.Helper()
	fixture := &lifecycleFixture{endpoint: "192.0.2.8:44772"}
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	client, err := shared.NewLifecycleClient(server.Client(), shared.Config{Address: server.URL + "/v1", APIKey: "lifecycle-secret"})
	require.NoError(t, err)
	kubernetes := fake.NewSimpleClientset()
	sandboxes, err := NewSandboxes(client, config())
	require.NoError(t, err)
	vault := &vaultFixture{}
	egress := httptest.NewServer(vault)
	t.Cleanup(egress.Close)
	fixture.egress = egress.URL
	fixture.vault = vault
	return sandboxes, kubernetes, fixture
}

func TestNativeSandboxSubmissionRetainsHomeAndWorkspaceWithoutExecutor(t *testing.T) {
	provider, _, fixture := sandboxClient(t)
	input := request()
	id, err := provider.submitRequest(context.Background(), input, "execd-secret")
	require.NoError(t, err)
	require.Equal(t, sandboxID, id)
	spec := fixture.create
	require.Equal(t, "native-claude:test", spec.Image.URI)
	require.Equal(t, []string{"sleep", "infinity"}, spec.Entrypoint)
	require.Nil(t, spec.Timeout)
	require.Equal(t, "execd-secret", spec.Env["EXECD_ACCESS_TOKEN"])
	require.Equal(t, input.WorkID, spec.Env["TALGENT_WORK_ID"])
	require.NotContains(t, spec.Env, workCredentialEnv, "the Work credential reaches the Gateway through the Credential Proxy, never the sandbox")
	for _, retired := range []string{"TALGENT_EXECUTION_ID", "TALGENT_EXECUTION_PROOF", "TALGENT_AGENT_SESSION_ID"} {
		require.NotContains(t, spec.Env, retired)
	}

	require.Equal(t, "/home/talgent", spec.Env["HOME"])
	require.Equal(t, "/home/talgent/.claude", spec.Env["CLAUDE_CONFIG_DIR"])
	require.Regexp(t, "^[a-f0-9]{64}$", spec.Env["CLAUDE_CODE_PROJECT_DIR_NAME"])
	projectDirectory := spec.Env["CLAUDE_CODE_PROJECT_DIR_NAME"]
	require.Equal(t, "500m", spec.ResourceRequests["cpu"])
	require.Equal(t, "1Gi", spec.ResourceLimits["memory"])
	require.Len(t, spec.Volumes, 2)
	paths := map[string]string{}
	claims := map[string]string{}
	for _, mount := range spec.Volumes {
		claims[mount.MountPath] = mount.PVC.ClaimName
		require.NotNil(t, mount.PVC.DeleteOnSandboxTermination)
		require.False(t, *mount.PVC.DeleteOnSandboxTermination)
		require.False(t, *mount.PVC.CreateIfNotExists)
		require.Nil(t, mount.Host)
		paths[mount.MountPath] = mount.SubPath
	}
	// Storage chose each space; the sandbox mounts its grants as issued.
	require.Equal(t, map[string]string{"/workspace": "nas-root", "/home/talgent": "nas-root"}, claims)
	require.Equal(t, map[string]string{"/workspace": "workspaces/key", "/home/talgent": "homes/key"}, paths)
	other := input
	other.WorkID = "other-session"
	other.HarnessSessionReference = "new-context"
	other.HomeBinding.Namespace = "../other/project"
	_, err = provider.submitRequest(context.Background(), other, "execd-secret-2")
	require.NoError(t, err)
	require.Regexp(t, "^[a-f0-9]{64}$", fixture.create.Env["CLAUDE_CODE_PROJECT_DIR_NAME"], "opaque namespaces must not become relative filesystem paths")
	require.NotEqual(t, projectDirectory, fixture.create.Env["CLAUDE_CODE_PROJECT_DIR_NAME"])
}

func TestSandboxLookupRejectsChangedSpecAndDuplicateAcrossPages(t *testing.T) {
	provider, _, fixture := sandboxClient(t)
	input := request()
	_, err := provider.submitRequest(context.Background(), input, "execd-secret")
	require.NoError(t, err)
	id, err := provider.lookupRequest(context.Background(), input, "execd-secret")
	require.NoError(t, err)
	require.Equal(t, sandboxID, id)
	changed := input
	changed.Capabilities.Prompt = "changed"
	_, err = provider.lookupRequest(context.Background(), changed, "execd-secret")
	require.ErrorIs(t, err, errEnvironmentConflict)
	fixture.pages = [][]sdk.SandboxInfo{{*fixture.info}, {*fixture.info}}
	_, err = provider.lookupRequest(context.Background(), input, "execd-secret")
	require.ErrorIs(t, err, errEnvironmentConflict)
	require.Equal(t, 1, fixture.creates, "lookup must never allocate")
}

func TestSandboxSurplusCleanupRechecksImmutableOwnershipBeforeDelete(t *testing.T) {
	provider, _, fixture := sandboxClient(t)
	input := request()
	spec, err := provider.specification(context.Background(), input, testMounts, owner(input), "execd-secret", "work-credential-fixture")
	require.NoError(t, err)
	fixture.pages = [][]sdk.SandboxInfo{{{ID: sandboxID, Metadata: spec.Metadata}}}
	other := Allocation{WorkID: input.WorkID, ID: "01900000-0000-7000-8000-000000000002"}
	fixture.info = &sdk.SandboxInfo{ID: sandboxID, Metadata: correlation(other)}
	require.ErrorIs(t, provider.prune(context.Background(), owner(input), spec, "", fixtureSandboxCredentials(nil, provider)), errEnvironmentConflict)
	require.Zero(t, fixture.deletes, "a stale list cannot authorize deleting another allocation")
	fixture.info.Metadata = correlation(owner(input))
	fixture.info.Metadata[sandboxSpecLabel] = "changed-specification"
	require.ErrorIs(t, provider.prune(context.Background(), owner(input), spec, "", fixtureSandboxCredentials(nil, provider)), errEnvironmentConflict)
	require.Zero(t, fixture.deletes, "correlation without the immutable specification is insufficient")
}

func TestSandboxCreateFailureDoesNotRetryOrExposeResponseBody(t *testing.T) {
	provider, _, fixture := sandboxClient(t)
	fixture.createFailure = true
	_, err := provider.submitRequest(context.Background(), request(), "execd-secret")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "must-not-leak-token")
	require.Equal(t, 1, fixture.creates)
}

func TestSandboxCreateRefusalIsOnlyAnInvalidSpecificationAnswer(t *testing.T) {
	for status, refused := range map[int]bool{http.StatusBadRequest: true, http.StatusUnprocessableEntity: true, http.StatusInternalServerError: false, http.StatusServiceUnavailable: false, http.StatusConflict: false} {
		provider, _, fixture := sandboxClient(t)
		fixture.refuseStatus = status
		_, err := provider.submitRequest(context.Background(), request(), "execd-secret")
		require.Error(t, err)
		require.Equal(t, refused, errors.Is(err, errCreationRefused), "HTTP %d", status)
		require.NotContains(t, err.Error(), "invalid parameters", "response bodies never leave the adapter")
	}
}

func TestSandboxWithoutInputSourceMountsNoInputsClaim(t *testing.T) {
	provider, _, _ := sandboxClient(t)
	input := request()
	spec, err := provider.specification(context.Background(), input, testMounts, owner(input), "test-token", "work-credential-fixture")
	require.NoError(t, err)
	for _, volume := range spec.Volumes {
		require.NotEqual(t, workInputsMountPath, volume.MountPath)
	}
}

func TestSandboxEndpointRequiresCurrentIdentityAndResolvesAgain(t *testing.T) {
	provider, _, fixture := sandboxClient(t)
	input := request()
	_, err := provider.submitRequest(context.Background(), input, "execd-secret")
	require.NoError(t, err)
	endpoint, ready, err := provider.Endpoint(context.Background(), owner(input), sandboxID)
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "http://192.0.2.8:44772", endpoint)
	fixture.endpoint = "192.0.2.9:44772"
	endpoint, ready, err = provider.Endpoint(context.Background(), owner(input), sandboxID)
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "http://192.0.2.9:44772", endpoint)
	fixture.info.Status.State = sdk.StatePending
	_, ready, err = provider.Endpoint(context.Background(), owner(input), sandboxID)
	require.NoError(t, err)
	require.False(t, ready)
	other := Allocation{WorkID: input.WorkID, ID: "01900000-0000-7000-8000-000000000002"}
	_, _, err = provider.Endpoint(context.Background(), other, sandboxID)
	require.ErrorIs(t, err, errEnvironmentConflict)
}

func TestSandboxDeletionUsesProviderAuthorityWithoutPodObservation(t *testing.T) {
	ctx := context.Background()
	provider, kubernetes, fixture := sandboxClient(t)
	input := request()
	_, err := provider.submitRequest(ctx, input, "execd-secret")
	require.NoError(t, err)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "native-pod", Namespace: "executions", Labels: map[string]string{"opensandbox.io/id": sandboxID}}}
	require.NoError(t, kubernetes.Tracker().Add(pod))
	other := owner(input)
	other.WorkID = "unrelated-session"
	_, err = provider.deleteLocked(ctx, other, sandboxID, fixtureSandboxCredentials(nil, provider))
	require.ErrorIs(t, err, errEnvironmentConflict)
	require.Zero(t, fixture.deletes)
	absent, err := provider.deleteLocked(ctx, owner(input), sandboxID, fixtureSandboxCredentials(nil, provider))
	require.NoError(t, err)
	require.False(t, absent, "DELETE acknowledgement is not absence")
	absent, err = provider.deleteLocked(ctx, owner(input), sandboxID, fixtureSandboxCredentials(nil, provider))
	require.NoError(t, err)
	require.True(t, absent, "the provider confirms environment deletion")
	require.Empty(t, kubernetes.Actions(), "Agent must not observe or mutate underlying Pods")
}

// Default structured details and explicit content/detail choices reach the
// actual native launch request without enabling a second usage producer.
func TestNativeTelemetryCollectionOptionsReachSandbox(t *testing.T) {
	for _, tc := range []struct {
		name             string
		environment      map[string]string
		details, content string
	}{
		{name: "defaults", details: "1", content: "0"},
		{name: "explicit", environment: map[string]string{"OTEL_LOG_TOOL_DETAILS": "0", "OTEL_LOG_TOOL_CONTENT": "1"}, details: "0", content: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, _, fixture := sandboxClient(t)
			settings := config()
			for key, value := range tc.environment {
				settings.Environment[key] = value
			}
			provider, err := NewSandboxes(base.client, settings)
			require.NoError(t, err)
			_, err = provider.submitRequest(context.Background(), request(), "execution-token-fixture")
			require.NoError(t, err)
			require.Equal(t, "otlp", fixture.create.Env["OTEL_LOGS_EXPORTER"])
			require.Equal(t, "none", fixture.create.Env["OTEL_METRICS_EXPORTER"])
			require.Equal(t, tc.details, fixture.create.Env["OTEL_LOG_TOOL_DETAILS"])
			require.Equal(t, tc.content, fixture.create.Env["OTEL_LOG_TOOL_CONTENT"])
		})
	}
}

// Continuing and fresh contexts both preserve the Work and trusted ownership;
// stale native/environment attribution must not overwrite the Work identity.
func TestNativeTelemetryAttributionAcrossContinueAndFresh(t *testing.T) {
	for _, mode := range []domain.ContextMode{domain.ContextContinue, domain.ContextFresh} {
		t.Run(string(mode), func(t *testing.T) {
			base, _, fixture := sandboxClient(t)
			settings := config()
			settings.Environment["OTEL_RESOURCE_ATTRIBUTES"] = "talgent.execution_id=stale,talgent.project_id=wrong"
			provider, err := NewSandboxes(base.client, settings)
			require.NoError(t, err)
			start := request()
			start.ContextMode = mode
			start.TraceAttributes = map[string]string{"talgent.project_id": "project", "talgent.case_id": "case", "talgent.agent_work_id": start.WorkID}
			_, err = provider.submitRequest(context.Background(), start, "execution-token-fixture")
			require.NoError(t, err)
			require.Equal(t, "talgent.agent_work_id=session,talgent.case_id=case,talgent.project_id=project", fixture.create.Env["OTEL_RESOURCE_ATTRIBUTES"], "telemetry is attributed per Work")
		})
	}
}
