package connectrpc_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"
	"github.com/talgent-work/contracts/gen/go/agent/public/v2/agentpublicv2connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
)

// recordingWorks is the neutral WorkService the retiring v1 service delegates
// to: it records what it was asked and answers fixed values.
type recordingWorks struct {
	agentpublicv2connect.UnimplementedWorkServiceHandler
	start   *agentpublicv2.StartWorkRequest
	library *agentpublicv2.ListLibraryFilesRequest
	err     error
}

var (
	created = timestamppb.New(time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC))
	updated = timestamppb.New(time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC))
	lost    = timestamppb.New(time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC))
)

func (r *recordingWorks) StartWork(_ context.Context, request *agentpublicv2.StartWorkRequest) (*agentpublicv2.StartWorkResponse, error) {
	r.start = request
	if r.err != nil {
		return nil, r.err
	}
	return &agentpublicv2.StartWorkResponse{WorkId: "w1"}, nil
}

func (r *recordingWorks) ListWorks(context.Context, *agentpublicv2.ListWorksRequest) (*agentpublicv2.ListWorksResponse, error) {
	return &agentpublicv2.ListWorksResponse{Works: []*agentpublicv2.WorkSummary{{
		WorkId: "w1", Title: "Plan", State: "stopped", Waiting: true, CreatedAt: created, UpdatedAt: updated, DisconnectedSince: lost, StateReason: "disconnected",
	}}}, nil
}

func (r *recordingWorks) ListLibraryFiles(_ context.Context, request *agentpublicv2.ListLibraryFilesRequest) (*agentpublicv2.ListLibraryFilesResponse, error) {
	r.library = request
	return &agentpublicv2.ListLibraryFilesResponse{
		Files: []*agentpublicv2.LibraryFile{{WorkId: "w1", WorkTitle: "Plan", Name: "plan.md", SizeBytes: 3, ContentType: "text/markdown", AddedAt: updated, SharedByUserId: "bob"}},
		Works: []*agentpublicv2.LibraryWork{{WorkId: "w1", Title: "Plan", SharedByUserId: "bob", Standalone: true}},
	}, nil
}

func (r *recordingWorks) SearchShareReaders(context.Context, *agentpublicv2.SearchShareReadersRequest) (*agentpublicv2.SearchShareReadersResponse, error) {
	return &agentpublicv2.SearchShareReadersResponse{Users: []*agentpublicv2.DirectoryUser{{UserId: "u2", Username: "bob", DisplayName: "Bob", AvatarUrl: "https://a/b.png"}}}, nil
}

// The retiring PersonalWorkService answers as WorkService does, every field
// carried across in both directions.
func TestPersonalWorkServiceCarriesEveryFieldOfWorkService(t *testing.T) {
	works := &recordingWorks{}
	handler := transport.NewPersonalWorkHandler(works)
	ctx := context.Background()

	start := &agentpublicv1.StartPersonalWorkRequest{
		RequestId: "r1", InitialMessage: "Draft the plan", RepositorySelections: []*agentpublicv1.RepositorySelection{{RepositoryId: "repo", BaseBranch: "main"}},
		CreationCapabilities: &agentpublicv1.CreationCapabilities{Prompt: proto.String("Be brief")}, Model: &agentpublicv1.WorkModelSelection{ConfigurationId: "m1"},
		ThinkingLevel: "high", ComputerId: "c1", Harness: &agentpublicv1.HarnessChoice{PermissionMode: agentpublicv1.PermissionMode_PERMISSION_MODE_PLAN},
	}
	started, err := handler.StartPersonalWork(ctx, start)
	require.NoError(t, err)
	require.Equal(t, "w1", started.GetWorkId())
	require.True(t, proto.Equal(&agentpublicv2.StartWorkRequest{
		RequestId: "r1", InitialMessage: "Draft the plan", RepositorySelections: start.GetRepositorySelections(), CreationCapabilities: start.GetCreationCapabilities(),
		Model: start.GetModel(), ThinkingLevel: "high", ComputerId: "c1", Harness: start.GetHarness(),
	}, works.start), "every start field reaches WorkService")

	listed, err := handler.ListPersonalWorks(ctx, &agentpublicv1.ListPersonalWorksRequest{})
	require.NoError(t, err)
	require.True(t, proto.Equal(&agentpublicv1.ListPersonalWorksResponse{Works: []*agentpublicv1.PersonalWorkSummary{{
		WorkId: "w1", Title: "Plan", State: "stopped", Waiting: true, CreatedAt: created, UpdatedAt: updated, DisconnectedSince: lost, StateReason: "disconnected",
	}}}, listed))

	library, err := handler.ListLibraryFiles(ctx, &agentpublicv1.ListLibraryFilesRequest{NameQuery: "plan", WorkId: "w1", AddedAfter: created, AddedBefore: lost})
	require.NoError(t, err)
	require.True(t, proto.Equal(&agentpublicv2.ListLibraryFilesRequest{NameQuery: "plan", WorkId: "w1", AddedAfter: created, AddedBefore: lost}, works.library))
	require.True(t, proto.Equal(&agentpublicv1.ListLibraryFilesResponse{
		Files: []*agentpublicv1.LibraryFile{{WorkId: "w1", WorkTitle: "Plan", Name: "plan.md", SizeBytes: 3, ContentType: "text/markdown", AddedAt: updated, SharedByUserId: "bob"}},
		Works: []*agentpublicv1.LibraryWork{{WorkId: "w1", Title: "Plan", SharedByUserId: "bob", Personal: true}},
	}, library))

	readers, err := handler.SearchShareReaders(ctx, &agentpublicv1.SearchShareReadersRequest{Query: "bo"})
	require.NoError(t, err)
	require.True(t, proto.Equal(&agentpublicv1.DirectoryUser{UserId: "u2", Username: "bob", DisplayName: "Bob", AvatarUrl: "https://a/b.png"}, readers.GetUsers()[0]))
}

// A refusal of WorkService reaches the v1 caller unchanged.
func TestPersonalWorkServiceKeepsWorkServiceRefusals(t *testing.T) {
	refusal := connect.NewError(connect.CodePermissionDenied, "refused")
	_, err := transport.NewPersonalWorkHandler(&recordingWorks{err: refusal}).StartPersonalWork(context.Background(), &agentpublicv1.StartPersonalWorkRequest{})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}
