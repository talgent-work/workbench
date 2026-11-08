package agent

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.uber.org/fx"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/business/agent/transport/workspacefile"
)

// workspaceReading reads a Work's workspace where its AgentWork locates it:
// Storage's Session Workspace on a Cloud, and, when the deployment reaches
// Managed Computers, the Work's directory on its machine, whose files are
// served behind Agent's own links.
var workspaceReading = fx.Options(
	fx.Provide(newManagedComputers, newWorkspaceFileSigner, newManagedWorkspaces, newWorkspaces),
	fx.Invoke(registerManagedWorkspaceFiles),
)

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

// newWorkspaceFileSigner signs Agent's links to Managed Works' files with a
// key derived from the control token only Agent and Computer's services
// hold; nil without Managed Computers, which issue no such link.
func newWorkspaceFileSigner(machines *execution.ManagedComputers, cfg ComputerConfig) (*application.WorkspaceFileSigner, error) {
	if machines == nil {
		return nil, nil
	}
	return application.NewWorkspaceFileSigner(cfg.WorkspaceFileAddress, cfg.Token, time.Now)
}

// newManagedWorkspaces reads Managed Works' directories through the relay,
// nil without Managed Computers. Storage judges their previews.
func newManagedWorkspaces(db *xorm.Engine, machines *execution.ManagedComputers, links *application.WorkspaceFileSigner, previews execution.PreviewJudge) (*execution.ManagedWorkspaces, error) {
	if machines == nil {
		return nil, nil
	}
	return execution.NewManagedWorkspaces(db, machines, links, previews)
}

func newWorkspaces(storage execution.StorageWorkspaces, managed *execution.ManagedWorkspaces) (application.WorkspacePort, error) {
	return execution.NewWorkspaces(storage, managed)
}

// registerManagedWorkspaceFiles serves the links to Managed Works' files,
// which only a deployment that reaches Managed Computers issues.
func registerManagedWorkspaceFiles(mux *http.ServeMux, links *application.WorkspaceFileSigner, workspaces application.WorkspacePort, works domain.AgentWorkRepository, logger *slog.Logger) error {
	if links == nil {
		return nil
	}
	opened, err := application.NewWorkspaceFileLinks(works, workspaces, links)
	if err != nil {
		return err
	}
	workspacefile.Register(mux, opened, logger)
	return nil
}
