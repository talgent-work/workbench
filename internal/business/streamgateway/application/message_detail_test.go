package application_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	streamrpc "github.com/talgent-work/workbench/internal/business/streamgateway/transport/connectrpc"
	"github.com/talgent-work/workbench/pkg/authn"
)

type workAccessFunc func(context.Context, string, string) error

func (f workAccessFunc) AuthorizeRead(ctx context.Context, actor string, _ query.ReaderAdmission, work string) error {
	return f(ctx, actor, work)
}

func (f workAccessFunc) AdmitStreamReader(ctx context.Context, actor string, _ query.ReaderAdmission, work string) (*query.WorkState, error) {
	return nil, f(ctx, actor, work)
}

func TestMessageReadsShareWorkAuthorizationAndBoundDetails(t *testing.T) {
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "viewer"})
	denied := true
	access := workAccessFunc(func(_ context.Context, actor, work string) error {
		require.Equal(t, "viewer", actor)
		require.Equal(t, "work", work)
		if denied {
			return query.ErrWorkAccessDenied
		}
		return nil
	})
	var requests []query.MessageDetailRequest
	store := &fakeStore{detailFn: func(_ context.Context, request query.MessageDetailRequest) (query.MessageDetail, error) {
		requests = append(requests, request)
		switch request.MessageID {
		case "missing":
			return query.MessageDetail{}, query.ErrMessageNotFound
		case "none":
			return query.MessageDetail{}, nil
		default:
			return query.MessageDetail{Format: 1}, nil
		}
	}}
	app := application.NewApplication(query.NewWatchStream(store, newFakeStream(), slogDiscard(t), access), query.NewListMessagesBySessionHandler(store, access), store, access)
	handler := streamrpc.NewHandler(app)
	_, err := handler.GetMessageDetail(ctx, connect.NewRequest(&streamgatewaypublicv1.GetMessageDetailRequest{AgentWorkId: "work", MessageId: "empty"}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = handler.ListMessagesBySession(ctx, connect.NewRequest(&streamgatewaypublicv1.ListMessagesBySessionRequest{AgentWorkId: "work"}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	err = app.Queries.WatchStream.Run(ctx, query.WatchStreamRequest{ActorUserID: "viewer", AgentWorkID: "work"}, func(query.PortalLivePayload) error { t.Fatal("denied stream sent data"); return nil })
	require.ErrorIs(t, err, query.ErrWorkAccessDenied)
	require.Empty(t, requests)
	require.Empty(t, store.recordedMessageCalls())
	require.Empty(t, store.recordedCalls())

	_, err = handler.GetToolDetail(ctx, connect.NewRequest(&streamgatewaypublicv1.GetToolDetailRequest{AgentWorkId: "work", ToolCallId: "tool"}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	denied = false
	store.toolDetailFn = func(_ context.Context, request query.ToolDetailRequest) (query.ToolDetail, error) {
		require.Equal(t, "tool", request.ToolCallID)
		require.EqualValues(t, 65536, request.Limit)
		require.EqualValues(t, 2, request.InputOffset)
		require.EqualValues(t, 3, request.OutputOffset)
		return query.ToolDetail{Input: &query.MessageDetail{Format: 2, Text: "original input"}, Output: &query.MessageDetail{Format: 1}}, nil
	}
	tool, err := handler.GetToolDetail(ctx, connect.NewRequest(&streamgatewaypublicv1.GetToolDetailRequest{AgentWorkId: "work", ToolCallId: "tool", InputOffset: 2, OutputOffset: 3, Limit: 1000000}))
	require.NoError(t, err)
	require.Equal(t, "original input", tool.Msg.Input.Text)
	require.NotNil(t, tool.Msg.Output)
	require.Empty(t, tool.Msg.Output.Text)
	_, err = handler.GetToolDetail(ctx, connect.NewRequest(&streamgatewaypublicv1.GetToolDetailRequest{AgentWorkId: "work", ToolCallId: " "}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	for _, tc := range []struct {
		id               string
		limit, wantLimit uint32
		format           dispatcherpublicv1.MessageDetailFormat
		code             connect.Code
	}{
		{id: "empty", wantLimit: 16384, format: dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_TEXT},
		{id: "none", limit: 1000000, wantLimit: 65536},
		{id: "missing", limit: 5, wantLimit: 5, code: connect.CodeNotFound},
	} {
		response, err := handler.GetMessageDetail(ctx, connect.NewRequest(&streamgatewaypublicv1.GetMessageDetailRequest{AgentWorkId: "work", MessageId: tc.id, Offset: 3, Limit: tc.limit}))
		if tc.code != 0 {
			require.Equal(t, tc.code, connect.CodeOf(err))
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.format, response.Msg.Format)
			require.Empty(t, response.Msg.Text)
		}
		last := requests[len(requests)-1]
		require.Equal(t, tc.wantLimit, last.Limit)
		require.EqualValues(t, 3, last.Offset)
	}
}

// A stored preview reaches the reader only when its shape is the called tool's.
func TestToolPreviewMatchesTheCalledTool(t *testing.T) {
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: "viewer"})
	access := workAccessFunc(func(context.Context, string, string) error { return nil })
	called := "Write"
	store := &fakeStore{toolDetailFn: func(context.Context, query.ToolDetailRequest) (query.ToolDetail, error) {
		return query.ToolDetail{
			Input: &query.MessageDetail{Format: 2}, Output: &query.MessageDetail{Format: 1, Text: "File created"}, ToolName: called,
			Preview: &query.ToolOutputPreview{ToolName: "Write", FilePath: "/workspace/a.md", Content: "# Written", TotalLength: 9,
				Hunks: []query.PatchHunk{{OldStart: 1, Lines: []string{"+x"}}}},
		}, nil
	}}
	app := application.NewApplication(query.NewWatchStream(store, newFakeStream(), slogDiscard(t), access), query.NewListMessagesBySessionHandler(store, access), store, access)
	handler := streamrpc.NewHandler(app)

	tool, err := handler.GetToolDetail(ctx, connect.NewRequest(&streamgatewaypublicv1.GetToolDetailRequest{AgentWorkId: "work", ToolCallId: "tool"}))
	require.NoError(t, err)
	require.Equal(t, "# Written", tool.Msg.Preview.GetContent())
	require.Equal(t, []string{"+x"}, tool.Msg.Preview.GetHunks()[0].GetLines())
	require.Equal(t, "File created", tool.Msg.Output.Text, "the original output stays readable")
	tool, err = handler.GetToolDetail(ctx, connect.NewRequest(&streamgatewaypublicv1.GetToolDetailRequest{AgentWorkId: "work", ToolCallId: "tool", OutputOffset: 16384}))
	require.NoError(t, err)
	require.Nil(t, tool.Msg.Preview, "later pages never resend the preview")

	called = "Bash"
	tool, err = handler.GetToolDetail(ctx, connect.NewRequest(&streamgatewaypublicv1.GetToolDetailRequest{AgentWorkId: "work", ToolCallId: "tool"}))
	require.NoError(t, err)
	require.Nil(t, tool.Msg.Preview, "a preview whose shape is another tool's is dropped")
}
