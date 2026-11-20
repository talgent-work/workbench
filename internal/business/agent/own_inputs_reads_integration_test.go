//go:build integration

package agent_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
)

// The Agent of any Work lists its own inputs and receives an Access Link for
// one of them through the public Work file API, with its Work's identity
// alone: a Standalone Work's inputs are its own, a Work with a File Home's
// are its File Home's. It reads no other Work's inputs.
func TestAWorksAgentReadsItsInputsOverThePublicAPI(t *testing.T) {
	m := newLifecycleModule(t).start()
	works := startCallerWorks(t, m)
	own, err := m.client.GetWork(m.control(), &agentprivatev1.GetWorkRequest{SubjectId: works.person, WorkId: works.standalone})
	require.NoError(t, err)
	created, err := m.client.CreateWork(m.control(), &agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), SubjectId: works.person, StartingUserId: works.person, AgentId: own.GetAgentId(),
		WorkCaller: &agentpublicv1.WorkCaller{Kind: "project", Id: readersProject}, WorkKey: &agentpublicv1.WorkKey{Kind: "case", Id: "case-inputs"},
		FileHome: &agentpublicv1.FileHome{Kind: "case", Id: "case-inputs"}})
	require.NoError(t, err)
	homed := created.GetWorkId()
	files := m.files()
	asAgent := func(workID, workCaller string) context.Context {
		ctx, _ := m.asAgentOf(workID, workCaller)
		return ctx
	}
	link := func(ctx context.Context, workID string) (*agentpublicv1.CreateWorkFileAccessLinkResponse, error) {
		return files.CreateWorkFileAccessLink(ctx, &agentpublicv1.CreateWorkFileAccessLinkRequest{WorkId: workID, Kind: agentpublicv1.WorkFileKind_WORK_FILE_KIND_ATTACHMENT, Name: "brief.md",
			Disposition: agentpublicv1.WorkFileAccessDisposition_WORK_FILE_ACCESS_DISPOSITION_DOWNLOAD})
	}

	for _, reader := range []struct{ work, caller string }{
		{works.standalone, "person:" + works.person},
		{works.application, "application:" + supportDesk},
		{homed, "project:" + readersProject},
	} {
		ctx := asAgent(reader.work, reader.caller)
		_, err = files.ListWorkFiles(ctx, &agentpublicv1.ListWorkFilesRequest{WorkId: reader.work})
		require.NoError(t, err, "the Agent of %s lists its inputs", reader.work)
		issued, err := link(ctx, reader.work)
		require.NoError(t, err, "the Agent of %s reads its inputs", reader.work)
		require.Equal(t, "access://inputs/brief.md", issued.GetAccessUrl())
	}

	ctx := asAgent(homed, "project:"+readersProject)
	_, err = files.ListWorkFiles(ctx, &agentpublicv1.ListWorkFilesRequest{WorkId: works.standalone})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "an Agent lists no other Work's inputs")
	_, err = link(ctx, works.standalone)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "an Agent reads no other Work's inputs")
	personal := asAgent(works.standalone, "person:"+works.person)
	_, err = files.ListWorkFiles(personal, &agentpublicv1.ListWorkFilesRequest{WorkId: homed})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "the Agent of the person's own Standalone Work lists no other Work's files")
	_, err = link(personal, homed)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "nor reads another Work's inputs")
}
