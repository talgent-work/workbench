// Package computer is Agent's client of Computer's admission contract.
package computer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/samber/oops"

	computerprivatev1 "github.com/talgent-work/contracts/gen/go/computer/private/v1"
	"github.com/talgent-work/contracts/gen/go/computer/private/v1/computerprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Config locates Computer and the control token Agent presents to it.
type Config struct {
	Address string `json:"address" yaml:"address" toml:"address"`
	Token   string `json:"token" yaml:"token" toml:"token"`
	// RelayAddress is Computer Gateway's in-cluster relay, through which
	// Agent and Dispatcher reach a Managed Computer's execd
	//; its environment PUT takes the same
	// control token. Without it, no Work starts on a Managed Computer.
	RelayAddress string `json:"relay-address" yaml:"relay-address" toml:"relay-address"`
	// MCPAddress is the platform Connector's public HTTPS address, where a
	// Managed Computer's local proxy sends its Works' Talgent Connector
	// calls; required with RelayAddress.
	MCPAddress string `json:"mcp-address" yaml:"mcp-address" toml:"mcp-address"`
	// ContentAddress is where the same Gateway serves Access Links on its
	// public origin, from which a Managed Computer downloads its Works'
	// inputs; required with RelayAddress.
	ContentAddress string `json:"content-address" yaml:"content-address" toml:"content-address"`
	// APIAddress is where a Managed Work calls the native API directly, on
	// the same public origin, through its local proxy; empty gives Managed
	// Works no direct API, as where Agents reach the platform only through
	// the Gateway.
	APIAddress string `json:"api-address" yaml:"api-address" toml:"api-address"`
	// WorkspaceFileAddress is where a reader's browser reaches Agent's links
	// to a Managed Work's files, such as /api/v1/agent/workspace-files;
	// required with RelayAddress. The links are signed with a key derived
	// from Token.
	WorkspaceFileAddress string `json:"workspace-file-address" yaml:"workspace-file-address" toml:"workspace-file-address"`
	// TelemetryAddress is the platform's public HTTPS OTLP endpoint, where a
	// Managed Work's Claude Code sends its telemetry with its run's
	// telemetry token; required with RelayAddress.
	TelemetryAddress string `json:"telemetry-address" yaml:"telemetry-address" toml:"telemetry-address"`
	// TelemetryCollectorAddress is the in-cluster OTLP HTTP base of the OTel
	// Collector, to which Agent forwards that telemetry once attributed;
	// required with RelayAddress.
	TelemetryCollectorAddress string `json:"telemetry-collector-address" yaml:"telemetry-collector-address" toml:"telemetry-collector-address"`
}

// Client asks Computer to admit each run of a Work and reports when the
// run's Desk leaves.
type Client struct {
	admission computerprivatev1connect.AdmissionServiceClient
	token     string
	logger    *slog.Logger
}

var (
	_ command.ComputerAdmissionPort   = (*Client)(nil)
	_ command.ComputerDescriptionPort = (*Client)(nil)
)

func NewClient(httpClient connecthttp.HTTPClient, config Config, logger *slog.Logger) (*Client, error) {
	if strings.TrimSpace(config.Address) == "" || strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("agent: Computer address and control token are required")
	}
	return &Client{admission: computerprivatev1connect.NewAdmissionServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, config.Address, connecthttp.WithReadMaxBytes(0)))), token: config.Token, logger: logger}, nil
}

// authorized is the context of a call under the control token.
func authorized(ctx context.Context, token string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	return ctx
}

func (c *Client) ListUsableComputers(ctx context.Context, personID string) ([]command.UsableComputer, error) {
	response, err := c.admission.ListUsableComputers(authorized(ctx, c.token), &computerprivatev1.ListUsableComputersRequest{PersonId: personID})
	if err != nil {
		return nil, callError(err)
	}
	computers := make([]command.UsableComputer, 0, len(response.GetComputers()))
	for _, computer := range response.GetComputers() {
		computers = append(computers, command.UsableComputer{ID: computer.GetId(), Cloud: computer.GetKind() == computerprivatev1.ComputerKind_COMPUTER_KIND_CLOUD})
	}
	return computers, nil
}

func (c *Client) AdmitRun(ctx context.Context, run command.RunAdmission) (command.ComputerAnswer, error) {
	response, err := c.admission.AdmitRun(authorized(ctx, c.token), &computerprivatev1.AdmitRunRequest{
		ComputerId: run.ComputerID, DeskId: run.DeskID, WorkId: run.WorkID, WorkOwnerId: run.WorkOwnerID, StarterId: run.StarterID, Sandbox: run.Sandbox,
	})
	if err != nil {
		return command.ComputerAnswer{}, callError(err)
	}
	switch response.GetAnswer() {
	case computerprivatev1.AdmissionAnswer_ADMISSION_ANSWER_SEATED:
		return command.ComputerAnswer{Kind: command.ComputerSeated}, nil
	case computerprivatev1.AdmissionAnswer_ADMISSION_ANSWER_WAITING:
		return command.ComputerAnswer{Kind: command.ComputerWaiting}, nil
	default:
		return command.ComputerAnswer{Kind: command.ComputerRejected, Rejection: rejection(response.GetReason(), response.GetComputerKind())}, nil
	}
}

func rejection(reason computerprivatev1.RejectionReason, kind computerprivatev1.ComputerKind) error {
	switch reason {
	case computerprivatev1.RejectionReason_REJECTION_REASON_FULL:
		// A full Cloud rejects as before Clouds were Computers.
		if kind == computerprivatev1.ComputerKind_COMPUTER_KIND_MANAGED {
			return domain.ErrComputerFull
		}
		return domain.ErrCapacityUnavailable
	case computerprivatev1.RejectionReason_REJECTION_REASON_NOT_USABLE:
		return domain.ErrComputerNotUsable
	case computerprivatev1.RejectionReason_REJECTION_REASON_UNREACHABLE:
		return domain.ErrComputerUnreachable
	case computerprivatev1.RejectionReason_REJECTION_REASON_REMOVED:
		return domain.ErrComputerRemoved
	case computerprivatev1.RejectionReason_REJECTION_REASON_OUTDATED:
		return domain.ErrComputerOutdated
	case computerprivatev1.RejectionReason_REJECTION_REASON_SANDBOX_UNAVAILABLE:
		return domain.ErrComputerNoSandbox
	default:
		return domain.ErrCapacityUnavailable
	}
}

func (c *Client) LeaveDesk(ctx context.Context, deskID string) error {
	if _, err := c.admission.LeaveDesk(authorized(ctx, c.token), &computerprivatev1.LeaveDeskRequest{DeskId: deskID}); err != nil {
		return callError(err)
	}
	return nil
}

// releaseTimeout bounds reporting an unused Desk; the caller's own request
// may already be ending.
const releaseTimeout = 5 * time.Second

func (c *Client) ReleaseUnusedDesk(ctx context.Context, deskID string) {
	if deskID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	if err := c.LeaveDesk(ctx, deskID); err != nil {
		c.logger.WarnContext(ctx, "unused desk left to reconciliation", slog.String("desk_id", deskID), slog.Any("error", err))
	}
}

func (c *Client) ListOpenDesks(ctx context.Context, openedBefore time.Time, afterDeskID string) ([]command.OpenDesk, error) {
	response, err := c.admission.ListOpenDesks(authorized(ctx, c.token), &computerprivatev1.ListOpenDesksRequest{OpenedBeforeUnixMs: openedBefore.UnixMilli(), AfterDeskId: afterDeskID})
	if err != nil {
		return nil, callError(err)
	}
	desks := make([]command.OpenDesk, 0, len(response.GetDesks()))
	for _, desk := range response.GetDesks() {
		desks = append(desks, command.OpenDesk{DeskID: desk.GetDeskId(), WorkID: desk.GetWorkId()})
	}
	return desks, nil
}

// DeskSeated asks Computer whether a Desk is seated now, and when. A Desk
// Computer does not know was never opened, so it is not seated.
func (c *Client) DeskSeated(ctx context.Context, deskID string) (time.Time, bool, error) {
	response, err := c.admission.GetDesk(authorized(ctx, c.token), &computerprivatev1.GetDeskRequest{DeskId: deskID})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, oops.Wrap(errors.Join(command.ErrComputerUnavailable, err))
	}
	if response.GetState() != computerprivatev1.DeskState_DESK_STATE_OCCUPIED {
		return time.Time{}, false, nil
	}
	var seatedAt time.Time
	if at := response.GetSeatedAt(); at != nil {
		seatedAt = at.AsTime()
	}
	return seatedAt, true, nil
}

// DescribeComputer asks Computer what a Computer is. An unknown Computer is
// one this Work cannot use.
func (c *Client) DescribeComputer(ctx context.Context, computerID string) (command.ComputerDescription, error) {
	response, err := c.admission.DescribeComputer(authorized(ctx, c.token), &computerprivatev1.DescribeComputerRequest{ComputerId: computerID})
	if err != nil {
		return command.ComputerDescription{}, callError(err)
	}
	description := command.ComputerDescription{Managed: response.GetKind() == computerprivatev1.ComputerKind_COMPUTER_KIND_MANAGED, Name: response.GetName(), Removed: response.GetRemoved(), Reachable: response.GetReachable()}
	if since := response.GetUnreachableSince(); since != nil && !description.Reachable {
		description.UnreachableSince = since.AsTime()
	}
	return description, nil
}

// callError keeps what Computer said: an unknown Computer is one this Work
// cannot use; anything else means Computer could not answer.
func callError(err error) error {
	if connect.CodeOf(err) == connect.CodeNotFound {
		return oops.Wrap(errors.Join(domain.ErrComputerNotUsable, err))
	}
	return oops.Wrap(errors.Join(command.ErrComputerUnavailable, err))
}
