//go:build integration

package dispatcher_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	agentapp "github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	agentexecution "github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	agenttransport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/business/dispatcher"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	transport "github.com/talgent-work/workbench/internal/business/dispatcher/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"golang.org/x/net/websocket"
)

// Real public/private clients, Dispatcher stream processing and Redis recovery.
// Work read authority/state and execd frames are controlled external boundaries;
// actual CLI command metadata is independently exercised by the native test.
func TestWorkCapabilityAPITracksNativeUpdatesAcrossDispatcherRecovery(t *testing.T) {
	replicas := newNativeReplicas(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	frames := make(chan string, 1)
	offsets := make(chan string, 2)
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		offsets <- ws.Request().URL.Query().Get("since")
		if websocket.Message.Send(ws, `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`) != nil {
			return
		}
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			var data []byte
			for websocket.Message.Receive(ws, &data) == nil {
			}
		}()
		for {
			select {
			case frame := <-frames:
				if websocket.Message.Send(ws, append([]byte{1}, frame...)) != nil {
					return
				}
			case <-closed:
				return
			case <-ctx.Done():
				return
			}
		}
	}))
	defer server.Close()
	target := command.ConnectExecution{WorkID: "capability-work", ProcessID: "process", NativeSessionID: uuid.NewString(), Endpoint: server.URL, AccessToken: "fixture"}
	ready := nativeprocess.NewProtocolState()
	ready.Ready = true
	replicas.seed(target, ready)
	records := &nativeRecords{}
	managers := 0
	newManager := func() *dispatcher.NativeExecutions {
		managers++
		return replicas.replica(fmt.Sprintf("replica-%d", managers), records, newTestDelivery(t, records, records))
	}
	manager := newManager()
	require.NoError(t, manager.Connect(ctx, target))
	permissions := &capabilityWorkAuthority{}
	permissions.state.Store("working")
	serve := func(manager *dispatcher.NativeExecutions) agentpublicv1connect.AgentWorkServiceClient {
		app := application.NewApplication(manager, nil, nil, nil, nil, nil, nil, nil, manager, nil)
		handler := connecttest.Handler(func(s *connect.Server) {
			dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, transport.NewExecutionHandler(app, "control"))
		})
		privateServer := httptest.NewServer(handler)
		t.Cleanup(privateServer.Close)
		control := agentexecution.NewDispatcherControl(privateServer.Client(), privateServer.URL, "control")
		agent := &agentapp.Application{Capabilities: control, WorkPrincipals: permissions, WorkViews: permissions}
		handler = connecttest.Handler(func(s *connect.Server) {
			agentpublicv1connect.RegisterAgentWorkServiceHandler(s, agenttransport.NewWorkHandler(agent))
		}, authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true}))
		publicServer := httptest.NewServer(handler)
		t.Cleanup(publicServer.Close)
		unauthenticated := dispatcherprivatev1connect.NewExecutionServiceClient(connect.NewClient(connecthttp.NewTransport(privateServer.Client(), privateServer.URL, connecthttp.WithReadMaxBytes(0))))
		_, err := unauthenticated.GetCapabilities(ctx, &dispatcherprivatev1.GetCapabilitiesRequest{WorkId: target.WorkID})
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		return agentpublicv1connect.NewAgentWorkServiceClient(connect.NewClient(connecthttp.NewTransport(publicServer.Client(), publicServer.URL, connecthttp.WithReadMaxBytes(0))))
	}
	client := serve(manager)
	read := func(actor, workID string) (*agentpublicv1.GetWorkCapabilitiesResponse, error) {
		request := &agentpublicv1.GetWorkCapabilitiesRequest{WorkId: workID}
		ctx, info := connect.NewClientContext(ctx)
		if actor != "" {
			info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
			info.RequestHeader().Set(authn.HeaderSubject, actor)
		}
		return client.GetWorkCapabilities(ctx, request)
	}
	_, err := read("", target.WorkID)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = read("bfe6534f-df01-5e55-b980-d252c7563446", target.WorkID)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", "another-work")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.Eventually(t, func() bool {
		value, err := read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", target.WorkID)
		return err == nil && !value.Observed && len(value.Commands) == 0
	}, 3*time.Second, 10*time.Millisecond)
	line := `{"type":"system","subtype":"commands_changed","commands":[{"name":"compact","description":"Native built-in","argumentHint":"[instructions]"},{"name":"plugin:check","description":"Native skill","argumentHint":"[path]"}]}` + "\n"
	frames <- line
	require.Eventually(t, func() bool {
		value, err := read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", target.WorkID)
		return err == nil && value.Observed && len(value.Commands) == 2
	}, 3*time.Second, 10*time.Millisecond)
	value, err := read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", target.WorkID)
	require.NoError(t, err)
	require.Equal(t, "plugin:check", value.Commands[1].Name)
	require.Equal(t, "Native skill", value.Commands[1].Description)
	require.Equal(t, "[path]", value.Commands[1].ArgumentHint)
	copy, err := manager.GetCapabilities(ctx, target.WorkID)
	require.NoError(t, err)
	copy.Commands[1].Name = "mutated"
	value, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", target.WorkID)
	require.NoError(t, err)
	require.Equal(t, "plugin:check", value.Commands[1].Name)
	// Another replica resumes the same process from the handed-over position. It
	// need not repeat initialize or reload Plugins to recover the inventory.
	require.NoError(t, manager.Shutdown(ctx))
	_, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", target.WorkID)
	require.Error(t, err, "a disconnected Harness must not return stale candidates as current")
	manager = newManager()
	require.NoError(t, manager.Connect(ctx, target))
	client = serve(manager)
	require.Eventually(t, func() bool {
		value, err := read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", target.WorkID)
		return err == nil && value.Observed && len(value.Commands) == 2
	}, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, "0", <-offsets)
	require.NotEqual(t, "0", <-offsets)
	frames <- `{"type":"system","subtype":"commands_changed","commands":[]}` + "\n"
	require.Eventually(t, func() bool {
		value, err := read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", target.WorkID)
		return err == nil && value.Observed && len(value.Commands) == 0
	}, 3*time.Second, 10*time.Millisecond)
	freshTarget := target
	freshTarget.ProcessID = "new-process"
	fresh := replicas.stored(freshTarget)
	require.False(t, fresh.Capabilities.Observed, "a different process must report its own actual candidates")
	require.Empty(t, fresh.Capabilities.Commands)
	permissions.state.Store("stopped")
	_, err = read("0ba1b686-0dfe-53b5-9f82-930f94137cd8", target.WorkID)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

type capabilityWorkAuthority struct{ state atomic.Value }

// AuthorizeWork admits only the Work owner, as the gateway authenticated it.
func (*capabilityWorkAuthority) AuthorizeWork(_ context.Context, actor, workID string) error {
	if actor != "0ba1b686-0dfe-53b5-9f82-930f94137cd8" || workID != "capability-work" {
		return access.ErrAccessDenied
	}
	return nil
}

func (a *capabilityWorkAuthority) GetWorkView(context.Context, string) (query.WorkView, error) {
	return query.WorkView{State: a.state.Load().(string)}, nil
}
