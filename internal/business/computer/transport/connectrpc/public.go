package connectrpc

import (
	"context"
	"errors"
	"net/url"
	"time"

	"connectrpc.com/connect"

	computerpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/computer/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/computer/public/v1/computerpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
	"github.com/talgent-work/workbench/pkg/authn"
)

// RegistrationHandler serves Talgent Computer before it holds a credential;
// the device code is its only proof.
type RegistrationHandler struct {
	application     *application.Application
	verificationURI string
}

var _ computerpublicv1connect.RegistrationServiceHandler = (*RegistrationHandler)(nil)

// NewRegistrationHandler serves registration; verificationURI is the
// browser page where people approve a user code.
func NewRegistrationHandler(app *application.Application, verificationURI string) *RegistrationHandler {
	return &RegistrationHandler{application: app, verificationURI: verificationURI}
}

func (h *RegistrationHandler) StartRegistration(ctx context.Context, req *connect.Request[computerpublicv1.StartRegistrationRequest]) (*connect.Response[computerpublicv1.StartRegistrationResponse], error) {
	started, err := h.application.StartRegistration(ctx, req.Msg.GetMachineName())
	if err != nil {
		return nil, publicError(err)
	}
	display := started.UserCode[:4] + "-" + started.UserCode[4:]
	complete := h.verificationURI + "?" + url.Values{"code": {display}}.Encode()
	return connect.NewResponse(&computerpublicv1.StartRegistrationResponse{
		DeviceCode: started.DeviceCode, UserCode: display, VerificationUri: h.verificationURI, VerificationUriComplete: complete,
		ExpiresInSeconds: int32(time.Until(started.ExpiresAt).Round(time.Second).Seconds()), IntervalSeconds: int32(started.Interval.Seconds()),
	}), nil
}

func (h *RegistrationHandler) RedeemRegistration(ctx context.Context, req *connect.Request[computerpublicv1.RedeemRegistrationRequest]) (*connect.Response[computerpublicv1.RedeemRegistrationResponse], error) {
	if req.Msg.GetDeviceCode() == "" {
		return nil, publicError(application.ErrRegistrationNotFound)
	}
	redemption, err := h.application.RedeemRegistration(ctx, req.Msg.GetDeviceCode())
	if err != nil {
		return nil, publicError(err)
	}
	if redemption.Pending {
		return connect.NewResponse(&computerpublicv1.RedeemRegistrationResponse{Status: computerpublicv1.RedemptionStatus_REDEMPTION_STATUS_PENDING}), nil
	}
	return connect.NewResponse(&computerpublicv1.RedeemRegistrationResponse{
		Status: computerpublicv1.RedemptionStatus_REDEMPTION_STATUS_REGISTERED, ComputerId: redemption.ComputerID, Name: redemption.Name,
		Credential: redemption.Credential, CredentialExpiresAtUnixMs: redemption.ExpiresAt.UnixMilli(),
	}), nil
}

// ComputerHandler serves the signed-in person's own Managed Computers.
type ComputerHandler struct {
	application *application.Application
}

var _ computerpublicv1connect.ComputerServiceHandler = (*ComputerHandler)(nil)

func NewComputerHandler(app *application.Application) *ComputerHandler {
	return &ComputerHandler{application: app}
}

func person(ctx context.Context) (string, error) {
	id, ok := authn.CurrentUserID(ctx)
	if !ok {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("computer: sign-in required"))
	}
	return id, nil
}

func (h *ComputerHandler) ListComputers(ctx context.Context, _ *connect.Request[computerpublicv1.ListComputersRequest]) (*connect.Response[computerpublicv1.ListComputersResponse], error) {
	owner, err := person(ctx)
	if err != nil {
		return nil, err
	}
	computers, err := h.application.ListManagedComputers(ctx, owner)
	if err != nil {
		return nil, publicError(err)
	}
	response := &computerpublicv1.ListComputersResponse{}
	for _, computer := range computers {
		response.Computers = append(response.Computers, managedToProto(computer))
	}
	return connect.NewResponse(response), nil
}

func (h *ComputerHandler) ListUsableComputers(ctx context.Context, _ *connect.Request[computerpublicv1.ListUsableComputersRequest]) (*connect.Response[computerpublicv1.ListUsableComputersResponse], error) {
	starter, err := person(ctx)
	if err != nil {
		return nil, err
	}
	computers, err := h.application.ListUsableComputers(ctx, starter)
	if err != nil {
		return nil, publicError(err)
	}
	response := &computerpublicv1.ListUsableComputersResponse{}
	for _, computer := range computers {
		kind := computerpublicv1.UsableComputerKind_USABLE_COMPUTER_KIND_CLOUD
		if computer.Kind == domain.KindManaged {
			kind = computerpublicv1.UsableComputerKind_USABLE_COMPUTER_KIND_MANAGED
		}
		response.Computers = append(response.Computers, &computerpublicv1.UsableComputer{Id: computer.ID, Name: computer.Name, Kind: kind, Reachable: computer.Reachable, ClaudeCodeSandbox: computer.Sandbox})
	}
	return connect.NewResponse(response), nil
}

func (h *ComputerHandler) RenameComputer(ctx context.Context, req *connect.Request[computerpublicv1.RenameComputerRequest]) (*connect.Response[computerpublicv1.RenameComputerResponse], error) {
	owner, err := person(ctx)
	if err != nil {
		return nil, err
	}
	computer, err := h.application.RenameComputer(ctx, owner, req.Msg.GetComputerId(), req.Msg.GetName())
	if err != nil {
		return nil, publicError(err)
	}
	return connect.NewResponse(&computerpublicv1.RenameComputerResponse{Computer: managedToProto(computer)}), nil
}

func (h *ComputerHandler) SetRoomRules(ctx context.Context, req *connect.Request[computerpublicv1.SetRoomRulesRequest]) (*connect.Response[computerpublicv1.SetRoomRulesResponse], error) {
	owner, err := person(ctx)
	if err != nil {
		return nil, err
	}
	policy, ok := policyFromProto[req.Msg.GetAdmissionPolicy()]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("computer: an admission policy is required"))
	}
	computer, err := h.application.SetRoomRules(ctx, application.SetRoomRules{OwnerID: owner, ComputerID: req.Msg.GetComputerId(), Desks: req.Msg.GetCapacity(), Policy: policy})
	if err != nil {
		return nil, publicError(err)
	}
	return connect.NewResponse(&computerpublicv1.SetRoomRulesResponse{Computer: managedToProto(computer)}), nil
}

func (h *ComputerHandler) RemoveComputer(ctx context.Context, req *connect.Request[computerpublicv1.RemoveComputerRequest]) (*connect.Response[computerpublicv1.RemoveComputerResponse], error) {
	owner, err := person(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.application.RemoveComputer(ctx, owner, req.Msg.GetComputerId()); err != nil {
		return nil, publicError(err)
	}
	return connect.NewResponse(&computerpublicv1.RemoveComputerResponse{}), nil
}

var policyFromProto = map[computerpublicv1.AdmissionPolicy]domain.AdmissionPolicy{
	computerpublicv1.AdmissionPolicy_ADMISSION_POLICY_REJECT: domain.AdmissionReject,
	computerpublicv1.AdmissionPolicy_ADMISSION_POLICY_QUEUE:  domain.AdmissionQueue,
}

func policyToProto(policy domain.AdmissionPolicy) computerpublicv1.AdmissionPolicy {
	for wire, value := range policyFromProto {
		if value == policy {
			return wire
		}
	}
	return computerpublicv1.AdmissionPolicy_ADMISSION_POLICY_UNSPECIFIED
}

func (h *ComputerHandler) GetRegistration(ctx context.Context, req *connect.Request[computerpublicv1.GetRegistrationRequest]) (*connect.Response[computerpublicv1.GetRegistrationResponse], error) {
	approver, err := person(ctx)
	if err != nil {
		return nil, err
	}
	pending, err := h.application.GetRegistration(ctx, req.Msg.GetUserCode(), approver)
	if err != nil {
		return nil, publicError(err)
	}
	return connect.NewResponse(&computerpublicv1.GetRegistrationResponse{
		MachineName: pending.MachineName, ExpiresAtUnixMs: pending.ExpiresAt.UnixMilli(), Approved: pending.Approved,
	}), nil
}

func (h *ComputerHandler) ApproveRegistration(ctx context.Context, req *connect.Request[computerpublicv1.ApproveRegistrationRequest]) (*connect.Response[computerpublicv1.ApproveRegistrationResponse], error) {
	approver, err := person(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.application.ApproveRegistration(ctx, req.Msg.GetUserCode(), approver); err != nil {
		return nil, publicError(err)
	}
	return connect.NewResponse(&computerpublicv1.ApproveRegistrationResponse{}), nil
}

func managedToProto(computer application.ManagedComputer) *computerpublicv1.ManagedComputer {
	presence := &computerpublicv1.Presence{Reachable: computer.Presence.Reachable, NeverConnected: computer.NeverConnected}
	if !computer.Presence.Reachable && !computer.NeverConnected {
		presence.UnreachableSinceUnixMs = computer.Presence.UnreachableSince.UnixMilli()
	}
	return &computerpublicv1.ManagedComputer{
		Id: computer.ID, Name: computer.Name, Presence: presence, RegisteredAtUnixMs: computer.RegisteredAt.UnixMilli(),
		TalgentComputerVersion: computer.Versions.TalgentComputer, ClaudeCodeVersion: computer.Versions.ClaudeCode,
		Capacity: computer.Desks, AdmissionPolicy: policyToProto(computer.Policy), Running: computer.Occupied, Waiting: int64(computer.Waiting),
	}
}
