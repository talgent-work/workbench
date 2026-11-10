package execution

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

// cloudComputers describes every Computer as a Cloud.
type cloudComputers struct{ command.ComputerPort }

func (cloudComputers) DescribeComputer(context.Context, string) (command.ComputerDescription, error) {
	return command.ComputerDescription{}, nil
}

// managedPlace describes every Computer as a Managed Computer, removed when
// asked.
type managedPlace struct {
	command.ComputerPort
	removed bool
}

func (m *managedPlace) DescribeComputer(context.Context, string) (command.ComputerDescription, error) {
	return command.ComputerDescription{Managed: true, Name: managedComputerName, Removed: m.removed}, nil
}

// managedComputerName is what its person named the Managed Computer.
const managedComputerName = "Studio Mac, 2"

const managedComputerID = "01900000-0000-7000-8000-0000000000c1"

// managedGatewayAddress is the platform Connector's public address.
const managedGatewayAddress = "https://workbench.example/computer/v1/mcp"

// managedContentAddress is where the same Gateway serves Access Links.
const managedContentAddress = "https://workbench.example/computer/v1/storage/content"

// managedTelemetryAddress is the platform's public OTLP endpoint.
const managedTelemetryAddress = "https://workbench.example/computer/v1/otel"

// managedAPIAddress is where the same origin serves the native API.
const managedAPIAddress = "https://workbench.example/computer/v1/api"

func managedRequest() domain.LaunchSpecification {
	input := request()
	input.ComputerID = managedComputerID
	input.WorkModel = domain.WorkModelSelection{MachineLogin: true}
	input.LocatedLayout.Mounts = nil
	return input
}

// A Managed start on a Model Configuration holds its Model Template's
// environment except the endpoint, which the local proxy sets to itself, and
// never the key; it records the endpoint the proxy sends the requests to. A
// configuration no longer provided is never silently dropped.
func TestAManagedStartRealizesItsModelConfigurationWithoutEndpointOrKey(t *testing.T) {
	configured := managedRequest()
	configured.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
	spec, err := managedSpecificationFor(configured, fixtureModels(), "execd-token", "work-credential")
	require.NoError(t, err)
	require.Equal(t, "https://model.example/anthropic", spec.ModelEndpoint)
	require.Equal(t, map[string]string{"TALGENT_WORK_ID": "session", "DISABLE_AUTOUPDATER": "1",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "fixture-primary", "ANTHROPIC_DEFAULT_SONNET_MODEL": "fixture-primary", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "fixture-lightweight",
		"ANTHROPIC_DEFAULT_FABLE_MODEL": "fixture-primary", "CLAUDE_CODE_SUBAGENT_MODEL": "fixture-lightweight", "CLAUDE_CODE_EFFORT_LEVEL": "max"}, spec.Environment)
	encoded, err := json.Marshal(spec)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), fixtureModelKey, "the key is delivered, never recorded")

	configured.WorkModel = domain.WorkModelSelection{ConfigurationID: "withdrawn"}
	_, err = managedSpecificationFor(configured, fixtureModels(), "execd-token", "work-credential")
	require.ErrorIs(t, err, errModelConfigurationNotProvided)
}

func TestManagedEnvironmentHoldsNoMountsVaultHomeOrPlatformCredentials(t *testing.T) {
	spec, err := managedSpecificationFor(managedRequest(), fixtureModels(), "execd-token", "work-credential")
	require.NoError(t, err)
	require.Equal(t, managedComputerID, spec.ComputerID)
	require.Equal(t, map[string]string{"TALGENT_WORK_ID": "session", "DISABLE_AUTOUPDATER": "1"}, spec.Environment,
		"the machine supplies HOME, PATH and the Work root; no model, CA, proxy placeholder or Work credential reaches it")
	require.Equal(t, "work-credential", spec.WorkCredential, "the Work credential is issued and kept on the platform")
	require.NoError(t, spec.attachTelemetry(context.Background(), managedRequest(), managedTelemetryAddress, "telemetry-token-fixture"))
	encoded, err := json.Marshal(spec)
	require.NoError(t, err)
	decoded, err := decodeManagedSpecification(executionRecord{SpecificationJSON: encoded})
	require.NoError(t, err)
	require.Equal(t, spec, decoded)
	cloud, err := json.Marshal(map[string]any{"env": map[string]string{"EXECD_ACCESS_TOKEN": "x"}})
	require.NoError(t, err)
	_, err = decodeManagedSpecification(executionRecord{SpecificationJSON: cloud})
	require.ErrorIs(t, err, domain.ErrExecutionRequest, "a Cloud sandbox specification is no Managed environment")
	_, err = managedSpecificationFor(request(), fixtureModels(), "execd-token", "work-credential")
	require.ErrorIs(t, err, domain.ErrExecutionRequest, "a Managed start names its Computer")
}

// A Work of a Project on a Managed Computer mounts no Directory: its
// environment names its Project, from which the runtime plugin points it to
// the platform Connector's tools, and sets no TALGENT_KNOWLEDGE_ROOT.
func TestAManagedWorkOfAProjectNamesItsProjectAndNoKnowledgeRoot(t *testing.T) {
	input := managedRequest()
	input.ProjectID = "project"
	spec, err := managedSpecificationFor(input, fixtureModels(), "execd-token", "work-credential")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"TALGENT_WORK_ID": "session", "DISABLE_AUTOUPDATER": "1", "TALGENT_PROJECT_ID": "project"}, spec.Environment)
}

func TestManagedLaunchKeepsEverythingInsideTheWorkRoot(t *testing.T) {
	config := NativeLaunchConfig{Binary: "claude"}
	input := managedRequest()
	cloud, err := config.prepare(sandboxLayout, input, owner(input), "")
	require.NoError(t, err)
	managed, err := config.prepare(machineLayout, input, owner(input), "")
	require.NoError(t, err)
	require.Equal(t, cloud.SessionID, managed.SessionID, "the native session is the Work's on every Computer")
	require.Contains(t, managed.Command, "'--setting-sources' 'project,local'", "user-level settings stay excluded")
	require.Contains(t, managed.Command, `'{"mcpServers":{"talgent":{"headers":{"Authorization":"Bearer ${TALGENT_PROXY_TOKEN}"},"type":"http","url":"${TALGENT_PROXY_URL}/mcp"}}}'`, "the Talgent Connector is reached through the local proxy")
	require.Contains(t, cloud.Command, "talgent", "the Cloud launch keeps its Talgent Connector")
	require.True(t, strings.HasPrefix(managed.Command, managedRoot))
	require.Contains(t, managed.Command, `exec 2>>"$talgent_root/.talgent/"'talgent-native-`)
	require.NotContains(t, managed.Command, "/tmp/")
	require.NotContains(t, managed.PreparationCommand, "$HOME", "Plugin installation writes nothing to the machine's ~/.claude")
	require.NotContains(t, managed.PreparationCommand, "exec timeout")
	require.True(t, strings.HasSuffix(managed.PreparationCommand, " machine 2>/dev/null"), "checkouts use the machine's git credentials")
	require.Contains(t, cloud.PreparationCommand, `mkdir -p "${CLAUDE_CONFIG_DIR:?}" && exec timeout -k 5s`, "the Cloud preparation creates the Agent Harness's state")

	input.ContextMode = domain.ContextRecover
	recovered, err := config.prepare(machineLayout, input, owner(input), "")
	require.NoError(t, err)
	require.Contains(t, recovered.Command, `"${CLAUDE_CONFIG_DIR:-$HOME/.claude}"/projects/*/'`+recovered.SessionID+`.jsonl'`)
}

// A recovered Managed start resumes the conversation whose transcript the
// machine's Claude configuration holds, and otherwise starts it afresh.
func TestManagedRecoveredStartFindsTheTranscriptInTheMachinesHome(t *testing.T) {
	home := t.TempDir()
	run := func() string {
		script := recoveredManagedContext(domain.ClaudeCode, "session-id", []string{"--session-id", "session-id"}) + `printf '%s ' "$@"`
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
		output, err := cmd.Output()
		require.NoError(t, err)
		return strings.TrimSpace(string(output))
	}
	require.Equal(t, "--session-id session-id", run())
	project := filepath.Join(home, ".claude", "projects", "-Users-me-talgent-works-w1")
	require.NoError(t, os.MkdirAll(project, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(project, "session-id.jsonl"), []byte("{}\n"), 0o600))
	require.Equal(t, "--resume session-id", run())
}

// relayFixture serves the relay's environment PUT and the execd subset Agent
// calls for one Work.
type relayFixture struct {
	mu          sync.Mutex
	unreachable bool
	environment map[string]any
	credentials map[string]any
	forgotten   int
	deleted     []string
	paths       []string
}

func (f *relayFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	if f.unreachable {
		// The Gateway's own answer when it holds no connection of the machine.
		http.Error(w, "computer not connected", http.StatusServiceUnavailable)
		return
	}
	base := "/relay/" + managedComputerID + "/works/session"
	switch {
	case r.Method == http.MethodPut && r.URL.Path == base:
		if r.Header.Get("X-Talgent-Control-Token") != "control" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.environment = map[string]any{}
		_ = json.Unmarshal(body, &f.environment)
		_, _ = w.Write([]byte(`{"work_root":"/Users/me/.talgent/works/session"}`))
	case (r.Method == http.MethodPut || r.Method == http.MethodDelete) && r.URL.Path == base+"/credentials":
		if r.Header.Get("X-Talgent-Control-Token") != "control" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			f.credentials = nil
			f.forgotten++
		} else {
			body, _ := io.ReadAll(r.Body)
			f.credentials = map[string]any{}
			_ = json.Unmarshal(body, &f.credentials)
		}
		w.WriteHeader(http.StatusNoContent)
	case !strings.HasPrefix(r.URL.Path, base+"/"):
		// The machine holds nothing of any other Work.
		w.WriteHeader(http.StatusNotFound)
	case r.Header.Get("X-EXECD-ACCESS-TOKEN") != "execd-token":
		w.WriteHeader(http.StatusUnauthorized)
	case r.Method == http.MethodGet && r.URL.Path == base+"/ping":
		_, _ = w.Write([]byte(`{"reconciled":true}`))
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, base+"/pty/"):
		f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, base+"/pty/"))
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestManagedComputersReachTheWorkThroughTheRelay(t *testing.T) {
	ctx := context.Background()
	relay := &relayFixture{}
	server := httptest.NewServer(relay)
	defer server.Close()
	place := &managedPlace{}
	managed, err := NewManagedComputers(server.Client(), server.URL, ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, Telemetry: managedTelemetryAddress}, "control", place)
	require.NoError(t, err)
	spec, err := managedSpecificationFor(managedRequest(), fixtureModels(), "execd-token", "work-credential")
	require.NoError(t, err)

	require.NoError(t, managed.open(ctx, spec, "session"))
	require.Equal(t, "execd-token", relay.environment["access_token"])
	require.Equal(t, map[string]any{"TALGENT_WORK_ID": "session", "DISABLE_AUTOUPDATER": "1"}, relay.environment["environment"])
	require.NotContains(t, relay.environment, "model_proxy", "the machine's own Claude login is not proxied")
	configured := managedRequest()
	configured.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
	configuredSpec, err := managedSpecificationFor(configured, fixtureModels(), "execd-token", "work-credential")
	require.NoError(t, err)
	require.NoError(t, managed.open(ctx, configuredSpec, "session"))
	require.Equal(t, true, relay.environment["model_proxy"], "a Model Configuration's model always goes through the local proxy")
	require.NoError(t, managed.open(ctx, spec, "session"))

	reached, state, err := managed.resolve(ctx, spec, "session")
	require.NoError(t, err)
	require.Equal(t, environmentRunning, state)
	require.Equal(t, server.URL+"/relay/"+managedComputerID+"/works/session", reached.endpoint, "execd calls and Dispatcher use the relay base")
	require.True(t, reached.reconciled, "the ping reports the machine's round")
	_, state, err = managed.resolve(ctx, spec, "closed-work")
	require.NoError(t, err)
	require.Equal(t, environmentLost, state, "a Work the machine's reconciliation removed has lost its environment")

	released, err := managed.release(ctx, spec, "session", "preparation-handle", "", "process-handle")
	require.NoError(t, err)
	require.True(t, released)
	require.Equal(t, []string{"preparation-handle", "process-handle"}, relay.deleted, "the run's processes are released; the directory stays")
	released, err = managed.release(ctx, spec, "closed-work", "process-handle")
	require.NoError(t, err)
	require.True(t, released, "a Work the machine's reconciliation removed holds no process")
	require.Len(t, relay.deleted, 2)

	relay.unreachable = true
	require.ErrorIs(t, managed.open(ctx, spec, "session"), errMachineUnreachable)
	_, state, err = managed.resolve(ctx, spec, "session")
	require.NoError(t, err)
	require.Equal(t, environmentPending, state, "a machine the relay does not reach now is not lost")
	released, err = managed.release(ctx, spec, "session", "process-handle")
	require.NoError(t, err)
	require.False(t, released, "release waits for the machine")

	place.removed = true
	_, state, err = managed.resolve(ctx, spec, "session")
	require.NoError(t, err)
	require.Equal(t, environmentLost, state, "a removed Computer has lost the Work's environment")
	released, err = managed.release(ctx, spec, "session", "process-handle")
	require.NoError(t, err)
	require.True(t, released, "a removed machine has nothing left to release")
}

// On a Managed Computer the preparation script runs in the Work root: plan
// paths in the /workspace layout check out into it and are reported in that
// layout.
func TestPreparationChecksOutIntoTheWorkRoot(t *testing.T) {
	requireNodeAndGit(t)
	remote := gitRemote(t, t.TempDir(), "populated", "main")
	workRoot := t.TempDir()
	plan := []repositoryCheckout{
		{RepositoryID: "github:1", CloneURL: remote, BaseBranch: "main", WorkBranch: "talgent/TAL-1", TargetPath: "/workspace/repos/layout"},
	}
	encodedPlan, err := json.Marshal(plan)
	require.NoError(t, err)
	input := request()
	catalog := `{"name":"talgent-work-fixture","owner":{"name":"Talgent"},"plugins":[]}`
	process := exec.Command("node", "-e", nativePreparation, "claude", catalog, storageKey(input.WorkID + ":" + fixtureAllocationID)[:32], string(encodedPlan), "600", "machine")
	process.Dir = workRoot
	process.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_GLOBAL=/dev/null", "TALGENT_WORK_ROOT="+workRoot, "CLAUDE_CODE_PLUGIN_CACHE_DIR="+filepath.Join(workRoot, ".talgent", "claude-plugins"))
	output, err := process.Output()
	require.NoError(t, err)
	report := latestReport(output, 0, domain.CapabilitySnapshot{}, plan)
	require.Equal(t, []domain.RepositoryCheckout{
		{RepositoryID: "github:1", CheckedOut: true},
	}, report.checkouts(plan, domain.CheckoutIncomplete), "%s", output)
	require.FileExists(t, filepath.Join(workRoot, "repos", "layout", "README.md"))
}

// An execution's recorded realization chooses its environment authority; a
// Managed one in a deployment without the relay fails there, and an
// unrecorded realization is never taken for either.
func TestTheRecordedRealizationChoosesTheEnvironmentAuthority(t *testing.T) {
	cloudOnly := NewProvisioning(nil, nil, nil, NativeLaunchConfig{Binary: "claude"}, nil, nil)
	authority, err := cloudOnly.authority(executionRecord{Realization: realizationCloud})
	require.NoError(t, err)
	require.IsType(t, cloudEnvironment{}, authority)
	_, err = cloudOnly.authority(executionRecord{Realization: realizationManaged})
	require.ErrorIs(t, err, errManagedNotConfigured)
	require.ErrorIs(t, cloudOnly.specify(context.Background(), executionRecord{Realization: realizationManaged, SpecificationJSON: []byte("null")}, command.CreationSupply{}), errManagedNotConfigured, "a Managed execution's creation is refused before anything is specified")
	_, err = cloudOnly.authority(executionRecord{Realization: "kubernetes"})
	require.Error(t, err)

	machines, err := NewManagedComputers(http.DefaultClient, "http://relay.invalid", ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
	require.NoError(t, err)
	_, err = NewManagedEnvironment(&xorm.Engine{}, machines, nil, unusedWorks{}, time.Minute)
	require.Error(t, err, "the Managed realization needs the Model Configurations")
	_, err = NewManagedEnvironment(nil, machines, fixtureModels(), unusedWorks{}, time.Minute)
	require.Error(t, err, "the Managed realization keeps its marks on the executions")
	_, err = NewManagedEnvironment(&xorm.Engine{}, machines, fixtureModels(), unusedWorks{}, 0)
	require.Error(t, err, "inputs are refreshed at a positive interval")
	managed, err := NewManagedEnvironment(&xorm.Engine{}, machines, fixtureModels(), unusedWorks{}, time.Minute)
	require.NoError(t, err)
	authority, err = NewProvisioning(nil, nil, nil, NativeLaunchConfig{Binary: "claude"}, nil, managed).authority(executionRecord{Realization: realizationManaged})
	require.NoError(t, err)
	require.IsType(t, ManagedEnvironment{}, authority)
}

// unusedWorks stands in for the Works a test never reads.
type unusedWorks struct{ domain.AgentWorkRepository }

// The Work's credentials reach the machine through the relay with the control
// token; the machine forgets them on DELETE, and a machine away is transient
// as for every relay call.
func TestManagedComputersDeliverAndForgetTheWorksCredentials(t *testing.T) {
	ctx := context.Background()
	relay := &relayFixture{}
	server := httptest.NewServer(relay)
	defer server.Close()
	managed, err := NewManagedComputers(server.Client(), server.URL, ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
	require.NoError(t, err)
	spec, err := startedSpecification(managedRequest())
	require.NoError(t, err)
	credentials := workCredentials{Model: &workModelCredential{BaseURL: "https://model.example/anthropic", Key: "model-key"},
		Gateway: workGatewayCredential{URL: managed.gateway, WorkID: "session", IdentityToken: "agent-token", WorkCredential: "work-credential", SpaceToken: "space"}}
	require.NoError(t, managed.deliverCredentials(ctx, spec, "session", credentials))
	require.Equal(t, map[string]any{"model": map[string]any{"base_url": "https://model.example/anthropic", "key": "model-key"},
		"gateway": map[string]any{"url": managedGatewayAddress, "work_id": "session", "identity_token": "agent-token", "work_credential": "work-credential", "space_token": "space"}}, relay.credentials)

	credentials.Model, credentials.Gateway.SpaceToken = nil, ""
	require.NoError(t, managed.deliverCredentials(ctx, spec, "session", credentials))
	require.Equal(t, map[string]any{"gateway": map[string]any{"url": managedGatewayAddress, "work_id": "session", "identity_token": "agent-token", "work_credential": "work-credential"}}, relay.credentials,
		"the machine's own Claude login carries no model; a Work without an input source no Space Token")
	require.NoError(t, managed.forgetCredentials(ctx, spec, "session"))
	require.Nil(t, relay.credentials)
	require.Equal(t, 1, relay.forgotten)
	// A Work the machine's reconciliation removed forgot its credentials
	// with it: withdrawing them is done.
	document, err := json.Marshal(spec)
	require.NoError(t, err)
	gone := executionRecord{SessionID: "closed-work", SpecificationJSON: document}
	require.NoError(t, ManagedEnvironment{machines: managed}.withdraw(ctx, gone))

	relay.unreachable = true
	require.ErrorIs(t, managed.deliverCredentials(ctx, spec, "session", credentials), errMachineUnreachable)
	require.ErrorIs(t, managed.forgetCredentials(ctx, spec, "session"), errMachineUnreachable)
}

// The machine reaches the platform Connector over the internet with the Work
// credential, so only an HTTPS address is accepted.
func TestManagedComputersNeedTheGatewaysPublicHTTPSAddress(t *testing.T) {
	for _, address := range []string{"", "http://workbench.example/computer/v1/mcp", "https://user@workbench.example/mcp", "https://workbench.example/mcp?x=1", "/computer/v1/mcp"} {
		_, err := NewManagedComputers(http.DefaultClient, "http://relay.invalid", ManagedAddresses{Gateway: address, Content: managedContentAddress, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
		require.Error(t, err, address)
	}
}

// A Managed Work gets the native API where the platform serves it, and
// keeps what its start was realized with: the environment's platform_api
// and the delivered api credential both follow the recorded start, not a
// replica's current configuration.
func TestManagedWorksGetTheNativeAPIOnlyWhereItIsServed(t *testing.T) {
	ctx := context.Background()
	relay := &relayFixture{}
	server := httptest.NewServer(relay)
	defer server.Close()
	started := func(machines *ManagedComputers) managedSpecification {
		input := managedRequest()
		specified, err := ManagedEnvironment{machines: machines, models: fixtureModels()}.specify(ctx, input, testMounts, owner(input), "execd-token", "work-credential")
		require.NoError(t, err)
		spec, err := decodeManagedSpecification(executionRecord{SpecificationJSON: specified.document})
		require.NoError(t, err)
		return spec
	}
	with, err := NewManagedComputers(server.Client(), server.URL, ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, API: managedAPIAddress, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
	require.NoError(t, err)
	without, err := NewManagedComputers(server.Client(), server.URL, ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
	require.NoError(t, err)

	served := started(with)
	require.Equal(t, managedAPIAddress, served.APIAddress)
	require.NoError(t, without.open(ctx, served, "session"))
	require.Equal(t, true, relay.environment["platform_api"], "the start was realized with the native API")

	unserved := started(without)
	require.Empty(t, unserved.APIAddress)
	require.NoError(t, with.open(ctx, unserved, "session"))
	require.NotContains(t, relay.environment, "platform_api")

	for _, address := range []string{"https://other.example/computer/v1/api", "http://workbench.example/computer/v1/api", "https://workbench.example/", "https://workbench.example/api?x=1"} {
		_, err := NewManagedComputers(http.DefaultClient, "http://relay.invalid", ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, API: address, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
		require.Error(t, err, address)
	}
}

// The model digest follows the Model Configuration's current key on the
// endpoint the start was realized with; the machine login carries none.
func TestManagedModelDigestFollowsTheCurrentKey(t *testing.T) {
	record := func(model domain.WorkModelSelection) executionRecord {
		input := managedRequest()
		input.WorkModel = model
		spec, err := startedSpecification(input)
		require.NoError(t, err)
		document, err := json.Marshal(spec)
		require.NoError(t, err)
		request, err := encodeLaunch(input)
		require.NoError(t, err)
		return executionRecord{SpecificationJSON: document, RequestJSON: request}
	}
	authority := ManagedEnvironment{models: fixtureModels()}
	digest, err := authority.modelDigest(record(domain.WorkModelSelection{ConfigurationID: "default"}))
	require.NoError(t, err)
	require.Equal(t, modelCredential{Key: fixtureModelKey}.digest(), digest)
	digest, err = authority.modelDigest(record(domain.MachineLoginModel()))
	require.NoError(t, err)
	require.Empty(t, digest)
}

// startedSpecification is the specification a start records, as
// ManagedEnvironment.specify builds it, telemetry included.
func startedSpecification(request domain.LaunchSpecification) (managedSpecification, error) {
	spec, err := managedSpecificationFor(request, fixtureModels(), "execd-token", "work-credential")
	if err != nil {
		return spec, err
	}
	return spec, spec.attachTelemetry(context.Background(), request, managedTelemetryAddress, "telemetry-token-fixture")
}
