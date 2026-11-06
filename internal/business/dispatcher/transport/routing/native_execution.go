package routing

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	"connectrpc.com/connect"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

const PeerHopHeader = "X-Talgent-Peer-Hop"

var ErrPeerHopExceeded = errors.New("dispatcher: routing loop detected")

type nativePeerClients interface {
	ForAddress(string) dispatcherprivatev1connect.ExecutionServiceClient
}

// NativeExecutionHandler sends private commands to the replica reading the
// Work's stream, preserving authentication across one peer hop. Without a live
// owner the local replica answers; Connect then claims the stream.
type NativeExecutionHandler struct {
	next        dispatcherprivatev1connect.ExecutionServiceHandler
	owners      command.ExecutionOwners
	self, token string
	peers       nativePeerClients
}

var _ dispatcherprivatev1connect.ExecutionServiceHandler = (*NativeExecutionHandler)(nil)

func NewNativeExecutionHandler(next dispatcherprivatev1connect.ExecutionServiceHandler, owners command.ExecutionOwners, self, token string, peers nativePeerClients) *NativeExecutionHandler {
	return &NativeExecutionHandler{next: next, owners: owners, self: self, token: token, peers: peers}
}

func (h *NativeExecutionHandler) route(ctx context.Context, request connect.AnyRequest, workID string) (dispatcherprivatev1connect.ExecutionServiceClient, error) {
	if err := h.authenticate(request); err != nil {
		return nil, err
	}
	if strings.TrimSpace(workID) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, command.ErrExecutionConnection)
	}
	owner, exists, err := h.owners.Locate(ctx, workID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	if !exists || !owner.Alive || owner.Instance == h.self {
		return nil, nil
	}
	if request.Header().Get(PeerHopHeader) != "" {
		return nil, connect.NewError(connect.CodeUnavailable, ErrPeerHopExceeded)
	}
	return h.peers.ForAddress(owner.Addr), nil
}

func forwardNativeRequest[T any](request *connect.Request[T]) *connect.Request[T] {
	forwarded := connect.NewRequest(request.Msg)
	forwarded.Header().Set("Authorization", request.Header().Get("Authorization"))
	forwarded.Header().Set(PeerHopHeader, "1")
	for _, key := range []string{"Traceparent", "Tracestate"} {
		if value := request.Header().Get(key); value != "" {
			forwarded.Header().Set(key, value)
		}
	}
	return forwarded
}

func (h *NativeExecutionHandler) Connect(ctx context.Context, request *connect.Request[dispatcherprivatev1.ConnectRequest]) (*connect.Response[dispatcherprivatev1.ConnectResponse], error) {
	peer, err := h.route(ctx, request, request.Msg.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.Connect(ctx, request)
	}
	return peer.Connect(ctx, forwardNativeRequest(request))
}

// The input buffer lives in Redis, so any replica answers buffer requests
// itself; the Work's reader learns of a change from the buffer.
func (h *NativeExecutionHandler) authenticate(request connect.AnyRequest) error {
	if h.token == "" || subtle.ConstantTimeCompare([]byte(request.Header().Get("Authorization")), []byte("Bearer "+h.token)) != 1 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("native execution control authentication required"))
	}
	return nil
}

func (h *NativeExecutionHandler) EnqueueInput(ctx context.Context, request *connect.Request[dispatcherprivatev1.EnqueueInputRequest]) (*connect.Response[dispatcherprivatev1.EnqueueInputResponse], error) {
	if err := h.authenticate(request); err != nil {
		return nil, err
	}
	return h.next.EnqueueInput(ctx, request)
}

func (h *NativeExecutionHandler) ListBufferedInput(ctx context.Context, request *connect.Request[dispatcherprivatev1.ListBufferedInputRequest]) (*connect.Response[dispatcherprivatev1.ListBufferedInputResponse], error) {
	if err := h.authenticate(request); err != nil {
		return nil, err
	}
	return h.next.ListBufferedInput(ctx, request)
}

func (h *NativeExecutionHandler) DropInput(ctx context.Context, request *connect.Request[dispatcherprivatev1.DropInputRequest]) (*connect.Response[dispatcherprivatev1.DropInputResponse], error) {
	if err := h.authenticate(request); err != nil {
		return nil, err
	}
	return h.next.DropInput(ctx, request)
}

func (h *NativeExecutionHandler) Stop(ctx context.Context, request *connect.Request[dispatcherprivatev1.StopRequest]) (*connect.Response[dispatcherprivatev1.StopResponse], error) {
	peer, err := h.route(ctx, request, request.Msg.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.Stop(ctx, request)
	}
	return peer.Stop(ctx, forwardNativeRequest(request))
}

func (h *NativeExecutionHandler) SetThinkingLevel(ctx context.Context, request *connect.Request[dispatcherprivatev1.SetThinkingLevelRequest]) (*connect.Response[dispatcherprivatev1.SetThinkingLevelResponse], error) {
	peer, err := h.route(ctx, request, request.Msg.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.SetThinkingLevel(ctx, request)
	}
	return peer.SetThinkingLevel(ctx, forwardNativeRequest(request))
}

func (h *NativeExecutionHandler) SetPermissionMode(ctx context.Context, request *connect.Request[dispatcherprivatev1.SetPermissionModeRequest]) (*connect.Response[dispatcherprivatev1.SetPermissionModeResponse], error) {
	peer, err := h.route(ctx, request, request.Msg.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.SetPermissionMode(ctx, request)
	}
	return peer.SetPermissionMode(ctx, forwardNativeRequest(request))
}

func (h *NativeExecutionHandler) GetThinkingLevel(ctx context.Context, request *connect.Request[dispatcherprivatev1.GetThinkingLevelRequest]) (*connect.Response[dispatcherprivatev1.GetThinkingLevelResponse], error) {
	peer, err := h.route(ctx, request, request.Msg.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.GetThinkingLevel(ctx, request)
	}
	return peer.GetThinkingLevel(ctx, forwardNativeRequest(request))
}

func (h *NativeExecutionHandler) ListPendingInteractions(ctx context.Context, request *connect.Request[dispatcherprivatev1.ListPendingInteractionsRequest]) (*connect.Response[dispatcherprivatev1.ListPendingInteractionsResponse], error) {
	peer, err := h.route(ctx, request, request.Msg.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.ListPendingInteractions(ctx, request)
	}
	return peer.ListPendingInteractions(ctx, forwardNativeRequest(request))
}

func (h *NativeExecutionHandler) CancelTurn(ctx context.Context, request *connect.Request[dispatcherprivatev1.CancelTurnRequest]) (*connect.Response[dispatcherprivatev1.CancelTurnResponse], error) {
	peer, err := h.route(ctx, request, request.Msg.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.CancelTurn(ctx, request)
	}
	return peer.CancelTurn(ctx, forwardNativeRequest(request))
}

func (h *NativeExecutionHandler) GetCurrentTurn(ctx context.Context, request *connect.Request[dispatcherprivatev1.GetCurrentTurnRequest]) (*connect.Response[dispatcherprivatev1.GetCurrentTurnResponse], error) {
	peer, err := h.route(ctx, request, request.Msg.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.GetCurrentTurn(ctx, request)
	}
	return peer.GetCurrentTurn(ctx, forwardNativeRequest(request))
}

// ObserveProcess is answered from the memory of the replica reading the stream.
func (h *NativeExecutionHandler) ObserveProcess(ctx context.Context, request *connect.Request[dispatcherprivatev1.ObserveProcessRequest]) (*connect.Response[dispatcherprivatev1.ObserveProcessResponse], error) {
	peer, err := h.route(ctx, request, request.Msg.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.ObserveProcess(ctx, request)
	}
	return peer.ObserveProcess(ctx, forwardNativeRequest(request))
}
