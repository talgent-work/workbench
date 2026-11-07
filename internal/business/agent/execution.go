package agent

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	agentstorage "github.com/talgent-work/workbench/internal/business/agent/infrastructure/storage"
	"github.com/talgent-work/workbench/internal/business/agent/transport/telemetryingest"
	"github.com/talgent-work/workbench/internal/business/agent/transport/workspacefile"
	pkgkubernetes "github.com/talgent-work/workbench/internal/pkg/kubernetes"
	pkgmysql "github.com/talgent-work/workbench/internal/pkg/mysql"
	"github.com/talgent-work/workbench/internal/pkg/opensandbox"
	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"xorm.io/xorm"
)

type ExecutionClientConfig struct {
	Address string `json:"address" yaml:"address" toml:"address"`
	Token   string `json:"token" yaml:"token" toml:"token"`
}

type ExecutionConfig struct {
	NetworkAllow []string `json:"network-allow" yaml:"network-allow" toml:"network-allow"`
	// Models names the mounted Model Template and Model Configuration files.
	Models              execution.ModelCatalogConfig `json:"models" yaml:"models" toml:"models"`
	NetworkDeny         []string                     `json:"network-deny" yaml:"network-deny" toml:"network-deny"`
	CredentialTransport string                       `json:"credential-transport" yaml:"credential-transport" toml:"credential-transport"`
	OpenSandbox         opensandbox.Config           `json:"opensandbox" yaml:"opensandbox" toml:"opensandbox"`
	Native              execution.NativeLaunchConfig `json:"native" yaml:"native" toml:"native"`
	Image               string                       `json:"image" yaml:"image" toml:"image"`
	Requests            map[string]string            `json:"requests" yaml:"requests" toml:"requests"`
	Limits              map[string]string            `json:"limits" yaml:"limits" toml:"limits"`
	Environment         map[string]string            `json:"environment" yaml:"environment" toml:"environment"`
	Dispatcher          ExecutionClientConfig        `json:"dispatcher" yaml:"dispatcher" toml:"dispatcher"`
	// SandboxNamespace is where OpenSandbox creates Talgent's sandboxes.
	SandboxNamespace string `json:"sandbox-namespace" yaml:"sandbox-namespace" toml:"sandbox-namespace"`
	// SandboxCreationTimeout is the OpenSandbox server's sandbox creation
	// timeout (its sandbox_create_timeout_seconds); a create waits a little
	// longer for its answer. Empty is the server's default, five minutes.
	SandboxCreationTimeout Duration `json:"sandbox-creation-timeout" yaml:"sandbox-creation-timeout" toml:"sandbox-creation-timeout"`
}

func (cfg ExecutionConfig) Validate() error {
	if err := cfg.Native.Validate(); err != nil {
		return err
	}
	if err := cfg.Dispatcher.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.SandboxNamespace) == "" {
		return errors.New("agent: sandbox namespace is required")
	}
	return cfg.OpenSandbox.Validate()
}

func newExecutionSandboxes(cfg ExecutionConfig, client *sdk.LifecycleClient, models *execution.ModelCatalog) (*execution.Sandboxes, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	resources := corev1.ResourceRequirements{Requests: corev1.ResourceList{}, Limits: corev1.ResourceList{}}
	for name, value := range cfg.Requests {
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return nil, err
		}
		resources.Requests[corev1.ResourceName(name)] = quantity
	}
	for name, value := range cfg.Limits {
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return nil, err
		}
		resources.Limits[corev1.ResourceName(name)] = quantity
	}
	return execution.NewSandboxes(client, execution.SandboxConfig{CreationTimeout: cfg.SandboxCreationTimeout.Duration(), Image: cfg.Image, Resources: resources, Environment: cfg.Environment, Models: models, NetworkDeny: cfg.NetworkDeny, NetworkAllow: cfg.NetworkAllow})
}

var ExecutionModule = fx.Module("agent.execution-integration",
	pkgmysql.Bind("execution"),
	fx.Provide(
		func(cfg ExecutionConfig) (*execution.ModelCatalog, error) {
			return execution.LoadModelCatalog(cfg.Models)
		},
		newModelConfigurations,
		newExecutionSandboxes,
		newExecutionLifecycle,
		func(db *xorm.Engine, client *sdk.LifecycleClient, models *execution.ModelCatalog, repositories *application.RepositoryAccess, services *application.ServiceAccess, cfg ExecutionConfig) (*execution.SandboxCredentials, error) {
			return execution.NewSandboxCredentials(db, client, models, workRepositoryCredentials{app: repositories}, services, http.DefaultClient, cfg.CredentialTransport)
		},
		pkgkubernetes.NewClientset,
		newManagedComputers,
		newManagedEnvironment,
		newManagedWorkspaces,
		func(db *xorm.Engine, s *execution.Sandboxes, credentials *execution.SandboxCredentials, cfg ExecutionConfig, updates *workStateUpdates, requests command.InitialExecutionReader, managed *execution.ManagedEnvironment) *execution.Provisioning {
			p := execution.NewProvisioning(db, s, credentials, cfg.Native, time.Now, requests, managed)
			p.ProgressCommitted = updates.publish
			return p
		},
		fx.Annotate(execution.NewWorkCredentials, fx.As(new(query.WorkCredentialVerifier))),
		func(p *execution.Provisioning, credentials *execution.SandboxCredentials, cfg ExecutionConfig, checkouts *command.RecordRepositoryCheckoutsHandler) (*execution.NativeConnections, error) {
			return execution.NewNativeConnections(p, credentials, http.DefaultClient, cfg.Dispatcher.Address, cfg.Dispatcher.Token, checkouts)
		},
		fx.Annotate(newDispatcherControl, fx.As(fx.Self()), fx.As(new(command.RunningHarnessPort))),
	),
	fx.Invoke(registerManagedWorkspaceFiles),
	fx.Invoke(registerManagedTelemetry),
	environmentRecoveryOptions,
	RecoveryModule,
)

func (c ExecutionClientConfig) validate() error {
	u, err := url.Parse(c.Address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(c.Token) == "" {
		return errors.New("agent: authenticated HTTP(S) execution endpoint required")
	}
	return nil
}

func newDispatcherControl(client *http.Client, cfg ExecutionConfig) (*execution.DispatcherControl, error) {
	if client == nil {
		return nil, errors.New("agent: execution HTTP client required")
	}
	if err := cfg.Dispatcher.validate(); err != nil {
		return nil, err
	}
	return execution.NewDispatcherControl(client, cfg.Dispatcher.Address, cfg.Dispatcher.Token), nil
}

// newManagedComputers reaches Managed Computers through Computer Gateway's
// relay and gives them the platform Connector's public address. Without a
// relay address it is nil, and Provisioning fails every step of a Managed
// execution rather than run it anywhere else.
func newManagedComputers(cfg ComputerConfig, computers command.ComputerPort) (*execution.ManagedComputers, error) {
	if strings.TrimSpace(cfg.RelayAddress) == "" {
		return nil, nil
	}
	return execution.NewManagedComputers(relayClient(), cfg.RelayAddress, execution.ManagedAddresses{Gateway: cfg.MCPAddress, Content: cfg.ContentAddress, API: cfg.APIAddress, Telemetry: cfg.TelemetryAddress}, cfg.Token, computers)
}

// relayResponseTimeout bounds the wait for the relay's response headers; a
// file read's body is bounded per read by the Managed realization.
const relayResponseTimeout = 30 * time.Second

// relayClient reaches the relay, which serves HTTP/1.1 and WebSocket upgrades
// as execd does; a stalled relay never holds a call open.
func relayClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = relayResponseTimeout
	return &http.Client{Transport: transport}
}

// newManagedEnvironment is the Managed realization, nil without Managed
// Computers. Its Works' inputs are read through Storage's File Space
// Contract and synced again by the recovery sweep at its refresh interval.
func newManagedEnvironment(db *xorm.Engine, machines *execution.ManagedComputers, models *execution.ModelCatalog, sessions domain.AgentWorkRepository, client *http.Client, storage StorageConfig, recovery RecoveryConfig) (*execution.ManagedEnvironment, error) {
	if machines == nil {
		return nil, nil
	}
	inputs, err := agentstorage.NewWorkInputs(client, storage)
	if err != nil {
		return nil, err
	}
	refresh, err := recovery.inputsRefreshInterval()
	if err != nil {
		return nil, err
	}
	return execution.NewManagedEnvironment(db, machines, models, sessions, inputs, refresh)
}

// newManagedWorkspaces reads Managed Works' directories through the relay,
// nil without Managed Computers. Storage judges their previews. Its links are
// signed with a key derived from the control token only Agent and Computer's
// services hold.
func newManagedWorkspaces(db *xorm.Engine, machines *execution.ManagedComputers, cfg ComputerConfig, storage *agentstorage.Client) (*execution.ManagedWorkspaces, error) {
	if machines == nil {
		return nil, nil
	}
	links, err := execution.NewWorkspaceFileLinks(cfg.WorkspaceFileAddress, cfg.Token, time.Now)
	if err != nil {
		return nil, err
	}
	return execution.NewManagedWorkspaces(db, machines, links, storage)
}

// registerManagedWorkspaceFiles serves the links to Managed Works' files,
// which only a deployment that reaches Managed Computers issues.
func registerManagedWorkspaceFiles(mux *http.ServeMux, managed *execution.ManagedWorkspaces, workspaces application.WorkspacePort, works domain.AgentWorkRepository, logger *slog.Logger) error {
	if managed == nil {
		return nil
	}
	links, err := application.NewWorkspaceFileLinks(works, workspaces)
	if err != nil {
		return err
	}
	workspacefile.Register(mux, links, logger)
	return nil
}

// registerManagedTelemetry takes the telemetry of Works on Managed Computers,
// attributes it to each run and forwards it to the OTel Collector; nothing
// without Managed Computers.
func registerManagedTelemetry(mux *http.ServeMux, provisioning *execution.Provisioning, managed *execution.ManagedEnvironment, cfg ComputerConfig, logger *slog.Logger) error {
	if managed == nil {
		return nil
	}
	attributions, err := execution.NewTelemetryAttributions(provisioning)
	if err != nil {
		return err
	}
	collector, err := telemetryingest.NewCollector(http.DefaultClient, cfg.TelemetryCollectorAddress)
	if err != nil {
		return err
	}
	telemetryingest.Register(mux, attributions, collector, logger)
	return nil
}

// The adapter maps trusted Application results into native Vault bindings.
type workRepositoryCredentials struct{ app *application.RepositoryAccess }

func (s workRepositoryCredentials) Plan(ctx context.Context, owner, work string) ([]execution.RepositoryAuthorization, error) {
	refs, err := s.app.Plan(ctx, owner, work)
	if err != nil {
		return nil, err
	}
	result := make([]execution.RepositoryAuthorization, 0, len(refs))
	for _, ref := range refs {
		result = append(result, execution.RepositoryAuthorization{AccessID: ref.AccessID, RepositoryID: ref.RepositoryID, RepositoryHTTPSURL: ref.CloneURL})
	}
	return result, nil
}
func (s workRepositoryCredentials) Resolve(ctx context.Context, owner, work string) (execution.RepositoryResolution, error) {
	values, err := s.app.Resolve(ctx, owner, work)
	if err != nil {
		return execution.RepositoryResolution{}, err
	}
	result := execution.RepositoryResolution{}
	for _, v := range values.Credentials {
		result.Credentials = append(result.Credentials, execution.RepositoryCredential{AccessID: v.AccessID, RepositoryID: v.RepositoryID, RepositoryHTTPSURL: v.CloneURL, Username: v.Username, Password: v.Password, ExpiresAt: v.ExpiresAt})
		result.Statuses = append(result.Statuses, execution.ServiceAccessStatus{Service: "github", Target: "repository:" + v.RepositoryID, Status: "credential_available"})
	}
	for _, unavailable := range values.Unavailable {
		result.Statuses = append(result.Statuses, execution.ServiceAccessStatus{Service: "github", Target: "repository:" + unavailable.RepositoryID, Status: "unavailable", Reason: unavailable.Reason, Temporary: unavailable.Temporary})
	}
	return result, nil
}

func newExecutionLifecycle(cfg ExecutionConfig) (*sdk.LifecycleClient, error) {
	// OpenSandbox serves HTTP/1.1; internal RPC H2C cannot call it.
	return opensandbox.NewLifecycleClient(http.DefaultClient, cfg.OpenSandbox)
}
