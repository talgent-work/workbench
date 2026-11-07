// Package connectrpc serves Computer's private contracts.
package connectrpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	computerprivatev1 "github.com/talgent-work/contracts/gen/go/computer/private/v1"
	"github.com/talgent-work/contracts/gen/go/computer/private/v1/computerprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// AdmissionHandler serves the admission contract to Agent, authenticated by
// its control token.
type AdmissionHandler struct {
	application *application.Application
	token       controlToken
}

var _ computerprivatev1connect.AdmissionServiceHandler = (*AdmissionHandler)(nil)

func NewAdmissionHandler(app *application.Application, token string) *AdmissionHandler {
	return &AdmissionHandler{application: app, token: controlToken(token)}
}

func (h *AdmissionHandler) ListUsableComputers(ctx context.Context, req *computerprivatev1.ListUsableComputersRequest) (*computerprivatev1.ListUsableComputersResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetPersonId()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "computer: person is required")
	}
	computers, err := h.application.ListUsableComputers(ctx, req.GetPersonId())
	if err != nil {
		return nil, publicError(err)
	}
	response := &computerprivatev1.ListUsableComputersResponse{}
	for _, computer := range computers {
		response.Computers = append(response.Computers, &computerprivatev1.UsableComputer{Id: computer.ID, Kind: kindToProto(computer.Kind), Name: computer.Name})
	}
	return response, nil
}

func (h *AdmissionHandler) AdmitRun(ctx context.Context, req *computerprivatev1.AdmitRunRequest) (*computerprivatev1.AdmitRunResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	msg := req
	admission, err := h.application.AdmitRun(ctx, application.AdmitRun{ComputerID: msg.GetComputerId(), DeskID: msg.GetDeskId(), WorkID: msg.GetWorkId(), WorkOwnerID: msg.GetWorkOwnerId(), StarterID: msg.GetStarterId(), Sandbox: msg.GetSandbox()})
	if err != nil {
		return nil, publicError(err)
	}
	response := &computerprivatev1.AdmitRunResponse{Answer: answerToProto(admission.Answer), Reason: rejectionToProto(admission.Rejection)}
	if admission.Answer == application.AnswerRejected {
		response.ComputerKind = kindToProto(admission.Kind)
	}
	return response, nil
}

func (h *AdmissionHandler) LeaveDesk(ctx context.Context, req *computerprivatev1.LeaveDeskRequest) (*computerprivatev1.LeaveDeskResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if err := h.application.LeaveDesk(ctx, req.GetDeskId()); err != nil {
		return nil, publicError(err)
	}
	return &computerprivatev1.LeaveDeskResponse{}, nil
}

func (h *AdmissionHandler) ListOpenDesks(ctx context.Context, req *computerprivatev1.ListOpenDesksRequest) (*computerprivatev1.ListOpenDesksResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	msg := req
	desks, err := h.application.ListOpenDesks(ctx, time.UnixMilli(msg.GetOpenedBeforeUnixMs()), msg.GetAfterDeskId(), int(msg.GetLimit()))
	if err != nil {
		return nil, publicError(err)
	}
	response := &computerprivatev1.ListOpenDesksResponse{}
	for _, desk := range desks {
		response.Desks = append(response.Desks, &computerprivatev1.OpenDesk{DeskId: desk.DeskID, WorkId: desk.WorkID})
	}
	return response, nil
}

func (h *AdmissionHandler) DescribeComputer(ctx context.Context, req *computerprivatev1.DescribeComputerRequest) (*computerprivatev1.DescribeComputerResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetComputerId()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "computer: computer is required")
	}
	computer, err := h.application.DescribeComputer(ctx, req.GetComputerId())
	if err != nil {
		return nil, publicError(err)
	}
	response := &computerprivatev1.DescribeComputerResponse{Kind: kindToProto(computer.Kind), Name: computer.Name, OwnerId: computer.OwnerID, Removed: computer.Removed, Reachable: computer.Presence.Reachable}
	if !computer.Presence.Reachable && !computer.Presence.UnreachableSince.IsZero() {
		response.UnreachableSince = timestamppb.New(computer.Presence.UnreachableSince)
	}
	return response, nil
}

func (h *AdmissionHandler) GetDesk(ctx context.Context, req *computerprivatev1.GetDeskRequest) (*computerprivatev1.GetDeskResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := h.token.authenticate(info.RequestHeader().Get("Authorization")); err != nil {
		return nil, err
	}
	desk, err := h.application.Desk(ctx, req.GetDeskId())
	if errors.Is(err, domain.ErrDeskNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, "computer: desk not found")
	}
	if err != nil {
		return nil, publicError(err)
	}
	response := &computerprivatev1.GetDeskResponse{State: deskStates[desk.State]}
	if !desk.SeatedAt.IsZero() {
		response.SeatedAt = timestamppb.New(desk.SeatedAt)
	}
	return response, nil
}

var deskStates = map[domain.DeskState]computerprivatev1.DeskState{
	domain.DeskStateWaiting:  computerprivatev1.DeskState_DESK_STATE_WAITING,
	domain.DeskStateOccupied: computerprivatev1.DeskState_DESK_STATE_OCCUPIED,
	domain.DeskStateVacated:  computerprivatev1.DeskState_DESK_STATE_VACATED,
}

func kindToProto(kind domain.Kind) computerprivatev1.ComputerKind {
	if kind == domain.KindManaged {
		return computerprivatev1.ComputerKind_COMPUTER_KIND_MANAGED
	}
	return computerprivatev1.ComputerKind_COMPUTER_KIND_CLOUD
}

func answerToProto(answer application.Answer) computerprivatev1.AdmissionAnswer {
	switch answer {
	case application.AnswerSeated:
		return computerprivatev1.AdmissionAnswer_ADMISSION_ANSWER_SEATED
	case application.AnswerWaiting:
		return computerprivatev1.AdmissionAnswer_ADMISSION_ANSWER_WAITING
	default:
		return computerprivatev1.AdmissionAnswer_ADMISSION_ANSWER_REJECTED
	}
}

func rejectionToProto(rejection domain.Rejection) computerprivatev1.RejectionReason {
	switch rejection {
	case domain.RejectedFull:
		return computerprivatev1.RejectionReason_REJECTION_REASON_FULL
	case domain.RejectedNotUsable:
		return computerprivatev1.RejectionReason_REJECTION_REASON_NOT_USABLE
	case domain.RejectedUnreachable:
		return computerprivatev1.RejectionReason_REJECTION_REASON_UNREACHABLE
	case domain.RejectedRemoved:
		return computerprivatev1.RejectionReason_REJECTION_REASON_REMOVED
	case domain.RejectedOutdated:
		return computerprivatev1.RejectionReason_REJECTION_REASON_OUTDATED
	case domain.RejectedNoSandbox:
		return computerprivatev1.RejectionReason_REJECTION_REASON_SANDBOX_UNAVAILABLE
	default:
		return computerprivatev1.RejectionReason_REJECTION_REASON_UNSPECIFIED
	}
}
