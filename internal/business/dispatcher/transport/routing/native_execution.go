package routing

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
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

func (h *NativeExecutionHandler) route(ctx context.Context, workID string) (dispatcherprivatev1connect.ExecutionServiceClient, error) {
	if err := h.authenticate(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(workID) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, command.ErrExecutionConnection.Error()).WithCause(command.ErrExecutionConnection)
	}
	owner, exists, err := h.owners.Locate(ctx, workID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err.Error()).WithCause(err)
	}
	if !exists || !owner.Alive || owner.Instance == h.self {
		return nil, nil
	}
	if requestHeader(ctx).Get(PeerHopHeader) != "" {
		return nil, connect.NewError(connect.CodeUnavailable, ErrPeerHopExceeded.Error()).WithCause(ErrPeerHopExceeded)
	}
	return h.peers.ForAddress(owner.Addr), nil
}

// requestHeader is the inbound call's metadata.
func requestHeader(ctx context.Context) *connect.Header {
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		return info.RequestHeader()
	}
	return new(connect.Header)
}

// forwarded is the context of the call that hands the inbound call to its
// owner, with the inbound authentication and trace context, after one hop.
func forwarded(ctx context.Context) context.Context {
	inbound := requestHeader(ctx)
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", inbound.Get("Authorization"))
	info.RequestHeader().Set(PeerHopHeader, "1")
	for _, key := range []string{"Traceparent", "Tracestate"} {
		if value := inbound.Get(key); value != "" {
			info.RequestHeader().Set(key, value)
		}
	}
	return ctx
}

func (h *NativeExecutionHandler) Connect(ctx context.Context, request *dispatcherprivatev1.ConnectRequest) (*dispatcherprivatev1.ConnectResponse, error) {
	peer, err := h.route(ctx, request.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.Connect(ctx, request)
	}
	return peer.Connect(forwarded(ctx), request)
}

// The input buffer lives in Redis, so any replica answers buffer requests
// itself; the Work's reader learns of a change from the buffer.
func (h *NativeExecutionHandler) authenticate(ctx context.Context) error {
	if h.token == "" || subtle.ConstantTimeCompare([]byte(requestHeader(ctx).Get("Authorization")), []byte("Bearer "+h.token)) != 1 {
		return connect.NewError(connect.CodeUnauthenticated, "native execution control authentication required")
	}
	return nil
}

func (h *NativeExecutionHandler) EnqueueInput(ctx context.Context, request *dispatcherprivatev1.EnqueueInputRequest) (*dispatcherprivatev1.EnqueueInputResponse, error) {
	if err := h.authenticate(ctx); err != nil {
		return nil, err
	}
	return h.next.EnqueueInput(ctx, request)
}

func (h *NativeExecutionHandler) ListBufferedInput(ctx context.Context, request *dispatcherprivatev1.ListBufferedInputRequest) (*dispatcherprivatev1.ListBufferedInputResponse, error) {
	if err := h.authenticate(ctx); err != nil {
		return nil, err
	}
	return h.next.ListBufferedInput(ctx, request)
}

func (h *NativeExecutionHandler) DropInput(ctx context.Context, request *dispatcherprivatev1.DropInputRequest) (*dispatcherprivatev1.DropInputResponse, error) {
	if err := h.authenticate(ctx); err != nil {
		return nil, err
	}
	return h.next.DropInput(ctx, request)
}

func (h *NativeExecutionHandler) Stop(ctx context.Context, request *dispatcherprivatev1.StopRequest) (*dispatcherprivatev1.StopResponse, error) {
	peer, err := h.route(ctx, request.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.Stop(ctx, request)
	}
	return peer.Stop(forwarded(ctx), request)
}

func (h *NativeExecutionHandler) SetThinkingLevel(ctx context.Context, request *dispatcherprivatev1.SetThinkingLevelRequest) (*dispatcherprivatev1.SetThinkingLevelResponse, error) {
	peer, err := h.route(ctx, request.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.SetThinkingLevel(ctx, request)
	}
	return peer.SetThinkingLevel(forwarded(ctx), request)
}

func (h *NativeExecutionHandler) SetPermissionMode(ctx context.Context, request *dispatcherprivatev1.SetPermissionModeRequest) (*dispatcherprivatev1.SetPermissionModeResponse, error) {
	peer, err := h.route(ctx, request.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.SetPermissionMode(ctx, request)
	}
	return peer.SetPermissionMode(forwarded(ctx), request)
}

func (h *NativeExecutionHandler) GetThinkingLevel(ctx context.Context, request *dispatcherprivatev1.GetThinkingLevelRequest) (*dispatcherprivatev1.GetThinkingLevelResponse, error) {
	peer, err := h.route(ctx, request.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.GetThinkingLevel(ctx, request)
	}
	return peer.GetThinkingLevel(forwarded(ctx), request)
}

func (h *NativeExecutionHandler) ListPendingInteractions(ctx context.Context, request *dispatcherprivatev1.ListPendingInteractionsRequest) (*dispatcherprivatev1.ListPendingInteractionsResponse, error) {
	peer, err := h.route(ctx, request.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.ListPendingInteractions(ctx, request)
	}
	return peer.ListPendingInteractions(forwarded(ctx), request)
}

func (h *NativeExecutionHandler) CancelTurn(ctx context.Context, request *dispatcherprivatev1.CancelTurnRequest) (*dispatcherprivatev1.CancelTurnResponse, error) {
	peer, err := h.route(ctx, request.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.CancelTurn(ctx, request)
	}
	return peer.CancelTurn(forwarded(ctx), request)
}

func (h *NativeExecutionHandler) GetCurrentTurn(ctx context.Context, request *dispatcherprivatev1.GetCurrentTurnRequest) (*dispatcherprivatev1.GetCurrentTurnResponse, error) {
	peer, err := h.route(ctx, request.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.GetCurrentTurn(ctx, request)
	}
	return peer.GetCurrentTurn(forwarded(ctx), request)
}

// ObserveProcess is answered from the memory of the replica reading the stream.
func (h *NativeExecutionHandler) ObserveProcess(ctx context.Context, request *dispatcherprivatev1.ObserveProcessRequest) (*dispatcherprivatev1.ObserveProcessResponse, error) {
	peer, err := h.route(ctx, request.GetWorkId())
	if err != nil {
		return nil, err
	}
	if peer == nil {
		return h.next.ObserveProcess(ctx, request)
	}
	return peer.ObserveProcess(forwarded(ctx), request)
}
