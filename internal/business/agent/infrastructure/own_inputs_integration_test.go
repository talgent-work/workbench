//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/standalonework"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// A Work's own Agent lists the current Attachments of its located inputs and
// receives a fresh Access Link for one on each request, whether the inputs
// are its own File Space or its File Home's; it reads no other Work's inputs.
func TestAWorksAgentReadsItsOwnInputs(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	jdb, err := jimuxorm.New(db)
	require.NoError(t, err)
	spaces := &librarySpaces{}
	works, app := standaloneWorksFixture(t, jdb, spaces)
	owner := standalonework.Caller{UserID: "owner", DisplayName: "Owner"}

	standalone, err := works.Start(ctx, standalonework.StartWork{Caller: owner, RequestID: uuid.NewString(), InitialMessage: "Draft the plan"})
	require.NoError(t, err)
	definition, err := app.EnsureEmployeeAgent(ctx, "owner", "Owner")
	require.NoError(t, err)
	initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), WorkBranch: "talgent/TAL-2", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Build it"}}
	homed, err := app.Commands.CreateWork.Handle(ctx, command.CreateWork{StartingUserID: "owner", SubjectID: "owner", RequestID: initial.RequestID, AgentID: definition.AgentID,
		WorkCaller: domain.WorkCaller{Kind: "project", ID: uuid.NewString()}, WorkKey: domain.WorkKey{Kind: "case", ID: "case-y"}, FileHome: domain.FileHome{Kind: "case", ID: "case-y"},
		InitialExecution: &initial})
	require.NoError(t, err)
	stored, err := app.Works.Get(ctx, homed)
	require.NoError(t, err)
	require.Equal(t, caseFiles("case-y", "inputs"), stored.LocatedLayout.Inputs.Directory, "a Work with a File Home reads its inputs from its File Home")

	now := time.Now().UTC()
	spaces.files = []command.SpaceFile{
		{Directory: workFiles(standalone, "inputs"), Kind: command.FileKindAttachment, Name: "brief.md", SizeBytes: 12, UpdatedAt: now, ShareLinks: []command.ShareLink{{Token: "t"}}},
		{Directory: workFiles(standalone, "artifacts"), Kind: command.FileKindArtifact, Name: "plan.pdf", WorkID: standalone, UpdatedAt: now},
		{Directory: caseFiles("case-y", "inputs"), Kind: command.FileKindAttachment, Name: "spec.md", UpdatedAt: now},
		{Directory: caseFiles("case-y", "artifacts"), Kind: command.FileKindArtifact, Name: "design.md", WorkID: homed, UpdatedAt: now},
	}
	names := func(files []command.SpaceFile) []string {
		var out []string
		for _, file := range files {
			require.Equal(t, command.FileKindAttachment, file.Kind)
			require.Empty(t, file.ShareLinks, "an Agent sees no Share Links")
			out = append(out, file.Name)
		}
		return out
	}

	standaloneAgent := standalonework.Caller{Agent: &standalonework.ActingAgent{WorkID: standalone}}
	listed, err := works.ListFiles(ctx, standaloneAgent, standalone)
	require.NoError(t, err)
	require.Equal(t, []string{"brief.md"}, names(listed), "a Standalone Work's Agent lists its own inputs' Attachments")

	homedAgent := standalonework.Caller{Agent: &standalonework.ActingAgent{WorkID: homed}}
	listed, err = works.ListFiles(ctx, homedAgent, homed)
	require.NoError(t, err)
	require.Equal(t, []string{"spec.md"}, names(listed), "a Work with a File Home lists its File Home's Attachments as its inputs")

	for range 2 {
		_, err = works.IssueAccessLink(ctx, standaloneAgent, standalonework.WorkFile{WorkID: standalone, Kind: command.FileKindAttachment, Name: "brief.md"}, false)
		require.NoError(t, err)
	}
	_, err = works.IssueAccessLink(ctx, homedAgent, standalonework.WorkFile{WorkID: homed, Kind: command.FileKindAttachment, Name: "spec.md"}, false)
	require.NoError(t, err)
	require.Equal(t, []string{"work:" + standalone + "/inputs/brief.md", "work:" + standalone + "/inputs/brief.md", "file_home:case:case-y/inputs/spec.md"}, spaces.issued,
		"each request issues its own Access Link")

	_, err = works.ListFiles(ctx, homedAgent, standalone)
	require.ErrorIs(t, err, access.ErrAccessDenied, "an Agent lists no other Work's inputs")
	_, err = works.IssueAccessLink(ctx, homedAgent, standalonework.WorkFile{WorkID: standalone, Kind: command.FileKindAttachment, Name: "brief.md"}, false)
	require.ErrorIs(t, err, access.ErrAccessDenied, "an Agent reads no other Work's inputs")
	_, err = works.IssueAccessLink(ctx, standaloneAgent, standalonework.WorkFile{WorkID: homed, Kind: command.FileKindAttachment, Name: "spec.md"}, false)
	require.ErrorIs(t, err, access.ErrAccessDenied, "reading its person's Library, an Agent reads Artifacts only")
	require.Len(t, spaces.issued, 3, "a refused read issues no Access Link")
}
