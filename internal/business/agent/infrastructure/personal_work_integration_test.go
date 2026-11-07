//go:build integration

package infrastructure_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/personalwork"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// librarySpaces stands in for Storage's File Space Contract: a space opens
// under its owner's name and the files are the fixture's.
type librarySpaces struct {
	personalwork.FileSpacePort
	files  []personalwork.SpaceFile
	issued []string
}

func (s *librarySpaces) OpenFileSpace(_ context.Context, owner string) (domain.WorkInputSource, error) {
	return domain.WorkInputSource{Reference: "file-space/" + owner, SpaceToken: "token-" + owner}, nil
}

func (s *librarySpaces) ResolveOwners(_ context.Context, sources []string) ([]string, error) {
	owners := make([]string, len(sources))
	for i, source := range sources {
		owners[i], _ = strings.CutPrefix(source, "file-space/")
	}
	return owners, nil
}

func (s *librarySpaces) filesOf(owners ...string) []personalwork.SpaceFile {
	var out []personalwork.SpaceFile
	for _, owner := range owners {
		for _, file := range s.files {
			if file.Owner == owner {
				out = append(out, file)
			}
		}
	}
	return out
}

func (s *librarySpaces) ListFiles(_ context.Context, owner string) ([]personalwork.SpaceFile, error) {
	return s.filesOf(owner), nil
}

func (s *librarySpaces) ListOwnerFiles(_ context.Context, owners []string) ([]personalwork.SpaceFile, error) {
	return s.filesOf(owners...), nil
}

func (s *librarySpaces) IssueAccessLink(_ context.Context, owner string, kind personalwork.FileKind, name string, _ bool) (personalwork.FileLink, error) {
	s.issued = append(s.issued, owner+"/"+string(kind)+"/"+name)
	return personalwork.FileLink{URL: "https://files.invalid/" + name}, nil
}

func (s *librarySpaces) DeleteFile(context.Context, domain.WorkInputSource, string, personalwork.FileKind, string, string) error {
	return nil
}

type knownPeople map[string]personalwork.DirectoryUser

func (k knownPeople) SearchUsers(context.Context, string, int) ([]personalwork.DirectoryUser, error) {
	return nil, nil
}

func (k knownPeople) GetUsers(_ context.Context, ids []string) ([]personalwork.DirectoryUser, error) {
	var out []personalwork.DirectoryUser
	for _, id := range ids {
		if user, ok := k[id]; ok {
			out = append(out, user)
		}
	}
	return out, nil
}

func personalWorksFixture(t *testing.T, db *xorm.Engine, spaces *librarySpaces) (*personalwork.PersonalWorks, *application.Application) {
	t.Helper()
	var starts []domain.StartExecutionRequest
	options := agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})
	create, _ := creationCoordinator(db, infrastructure.NewWorkCreationRequests(db, time.Now), startupAuthority{}, &registrationResources{}, &starts, nil, options, &personalStartupCatalog{})
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	lookup := infrastructure.NewAgentDefinitionLookup(db)
	app := &application.Application{Definitions: definitions, DefinitionLookup: lookup, Works: infrastructure.NewAgentWorkRepository(db, time.Now), WorkViews: infrastructure.NewWorkQueryRepository(db), Commands: application.Commands{
		EnsureAgentDefinition: command.NewEnsureAgentDefinitionHandler(definitions, lookup),
		CreateRequestedWork:   create,
	}}
	directory := knownPeople{"reader": {UserID: "reader", Username: "reader"}}
	return personalwork.New(app, infrastructure.NewWorkQueryRepository(db), spaces, directory), app
}

// A person's Personal Works start under the definition Agent establishes for
// them, each with its own File Space; their list, sharing, Library and file
// access follow from the owner and Work Shares alone.
func TestPersonalWorksStartListShareAndFillTheLibrary(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	spaces := &librarySpaces{}
	works, app := personalWorksFixture(t, db, spaces)
	owner := personalwork.Person{UserID: "owner", DisplayName: "Owner"}
	reader := personalwork.Person{UserID: "reader"}
	stranger := personalwork.Person{UserID: "stranger"}

	_, err = works.Start(ctx, personalwork.StartPersonalWork{Person: owner, RequestID: "not-a-uuid", InitialMessage: "x"})
	require.ErrorIs(t, err, personalwork.ErrInvalidRequest)
	empty, err := works.List(ctx, owner, 0)
	require.NoError(t, err)
	require.Empty(t, empty, "a person without a definition has no Personal Works")

	request := uuid.NewString()
	first, err := works.Start(ctx, personalwork.StartPersonalWork{Person: owner, RequestID: request, InitialMessage: "Draft the plan\nwith details"})
	require.NoError(t, err)
	again, err := works.Start(ctx, personalwork.StartPersonalWork{Person: owner, RequestID: request, InitialMessage: "Draft the plan\nwith details"})
	require.NoError(t, err)
	require.Equal(t, first, again, "a repeated request returns the Work it started")
	second, err := works.Start(ctx, personalwork.StartPersonalWork{Person: owner, RequestID: uuid.NewString(), InitialMessage: "Summarize the notes"})
	require.NoError(t, err)

	stored, err := app.Works.Get(ctx, first)
	require.NoError(t, err)
	require.True(t, stored.Personal())
	require.Equal(t, "Draft the plan", stored.DisplayTitle)
	require.Equal(t, domain.WorkInputSource{Reference: "file-space/" + first, SpaceToken: "token-" + first}, stored.InputSource, "the File Space is named by the Work")
	require.Empty(t, stored.ReadOnlyMounts)

	require.NoError(t, works.ChangeTitle(ctx, owner, first, "Plan"))
	require.ErrorIs(t, works.ChangeTitle(ctx, stranger, first, "Mine"), domain.ErrControlRequesterNotOwner)
	listed, err := works.List(ctx, owner, 0)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	titles := map[string]string{listed[0].WorkID: listed[0].Title, listed[1].WorkID: listed[1].Title}
	require.Equal(t, map[string]string{first: "Plan", second: "Summarize the notes"}, titles)
	require.Equal(t, first, listed[0].WorkID, "the most recently updated Work comes first")
	_, err = db.Exec("UPDATE agent_session SET stop_reason='disconnected' WHERE id=?", second)
	require.NoError(t, err)
	listed, err = works.List(ctx, owner, 0)
	require.NoError(t, err)
	require.Equal(t, "disconnected", listed[1].StateReason, "the list carries why a Work stopped")

	require.ErrorIs(t, works.GrantShare(ctx, owner, first, "ghost"), personalwork.ErrReaderUnknown)
	require.NoError(t, works.GrantShare(ctx, owner, first, "reader"))

	// A Work with a Subject and a Project, whose File Space it shares with
	// its caller's other Works, shared with the reader within its Project.
	definition, err := app.EnsureEmployeeAgent(ctx, "owner", "Owner")
	require.NoError(t, err)
	project := uuid.NewString()
	initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), WorkBranch: "talgent/TAL-1", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Build it"}}
	subjectWork, err := app.Commands.CreateRequestedWork.Handle(ctx, command.CreateWork{StartingUserID: "owner", SubjectID: "owner", RequestID: initial.RequestID, AgentID: definition.AgentID,
		Subject: domain.WorkSubject{Kind: "case", ID: "case-x"}, ProjectID: project, WorkspaceReference: uuid.NewString(),
		InputSource: domain.WorkInputSource{Reference: "file-space/case-x", SpaceToken: "token-x"}, InitialExecution: &initial})
	require.NoError(t, err)
	require.NoError(t, app.GrantWorkShare(ctx, application.GrantWorkShare{RequesterID: "owner", WorkID: subjectWork, ReaderID: "reader", Admission: domain.ReaderAdmission{ProjectID: project}}))

	now := time.Now().UTC()
	spaces.files = []personalwork.SpaceFile{
		{Owner: first, Kind: personalwork.FileKindAttachment, Name: "brief.md", UpdatedAt: now},
		{Owner: first, Kind: personalwork.FileKindArtifact, Name: "plan.pdf", WorkID: first, UpdatedAt: now.Add(-time.Hour)},
		{Owner: second, Kind: personalwork.FileKindArtifact, Name: "notes.md", WorkID: second, UpdatedAt: now},
		{Owner: "case-x", Kind: personalwork.FileKindArtifact, Name: "design.md", WorkID: subjectWork, UpdatedAt: now.Add(-2 * time.Hour)},
		{Owner: "case-x", Kind: personalwork.FileKindArtifact, Name: "other.md", WorkID: uuid.NewString(), UpdatedAt: now},
		{Owner: "case-x", Kind: personalwork.FileKindAttachment, Name: "spec.md", UpdatedAt: now},
	}

	names := func(library personalwork.Library) []string {
		var out []string
		for _, file := range library.Files {
			out = append(out, file.File.Name)
		}
		return out
	}
	mine, err := works.Library(ctx, owner, personalwork.LibraryFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"notes.md", "plan.pdf"}, names(mine), "the owner's Library holds their Personal Works' Artifacts, newest first")
	shared, err := works.Library(ctx, reader, personalwork.LibraryFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"plan.pdf", "design.md"}, names(shared), "a shared Work contributes only the Artifacts it published")
	for _, file := range shared.Files {
		require.Equal(t, "owner", file.Work.SharedBy)
	}
	narrowed, err := works.Library(ctx, reader, personalwork.LibraryFilter{NameQuery: "DESIGN"})
	require.NoError(t, err)
	require.Equal(t, []string{"design.md"}, names(narrowed))
	narrowed, err = works.Library(ctx, reader, personalwork.LibraryFilter{WorkID: first, AddedBefore: now})
	require.NoError(t, err)
	require.Equal(t, []string{"plan.pdf"}, names(narrowed))
	none, err := works.Library(ctx, stranger, personalwork.LibraryFilter{})
	require.NoError(t, err)
	require.Empty(t, none.Files)

	_, err = works.IssueAccessLink(ctx, reader, personalwork.WorkFile{WorkID: first, Kind: personalwork.FileKindAttachment, Name: "brief.md"}, false)
	require.NoError(t, err, "a Work Share holder reads a Personal Work's Attachments")
	_, err = works.IssueAccessLink(ctx, reader, personalwork.WorkFile{WorkID: subjectWork, Kind: personalwork.FileKindArtifact, Name: "design.md"}, false)
	require.NoError(t, err)
	require.Equal(t, []string{first + "/attachment/brief.md", "case-x/artifact/design.md"}, spaces.issued)
	for _, refused := range []personalwork.WorkFile{
		{WorkID: subjectWork, Kind: personalwork.FileKindAttachment, Name: "spec.md"},
		{WorkID: subjectWork, Kind: personalwork.FileKindArtifact, Name: "other.md"},
	} {
		_, err = works.IssueAccessLink(ctx, reader, refused, false)
		require.ErrorIs(t, err, personalwork.ErrFileNotFound, refused.Name)
	}
	_, err = works.IssueAccessLink(ctx, stranger, personalwork.WorkFile{WorkID: first, Kind: personalwork.FileKindArtifact, Name: "plan.pdf"}, false)
	require.ErrorIs(t, err, access.ErrAccessDenied)

	require.ErrorIs(t, works.DeleteFile(ctx, reader, personalwork.WorkFile{WorkID: first, Kind: personalwork.FileKindAttachment, Name: "brief.md"}), access.ErrAccessDenied)
	require.NoError(t, works.DeleteFile(ctx, owner, personalwork.WorkFile{WorkID: first, Kind: personalwork.FileKindAttachment, Name: "brief.md"}))
	require.ErrorIs(t, works.DeleteFile(ctx, owner, personalwork.WorkFile{WorkID: subjectWork, Kind: personalwork.FileKindArtifact, Name: "design.md"}), domain.ErrWorkFilesUnavailable)

	// A Closed Work's Artifacts stay in the Library; a revoked Work Share
	// takes its Work's files out of the reader's.
	_, err = db.Exec("UPDATE agent_session SET state=7 WHERE id=?", first)
	require.NoError(t, err)
	mine, err = works.Library(ctx, owner, personalwork.LibraryFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"notes.md", "plan.pdf"}, names(mine))
	_, err = app.RevokeWorkShare(ctx, application.RevokeWorkShare{ActorUserID: "owner", WorkID: first, ReaderID: "reader"})
	require.NoError(t, err)
	shared, err = works.Library(ctx, reader, personalwork.LibraryFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"design.md"}, names(shared))
	_, err = works.IssueAccessLink(ctx, reader, personalwork.WorkFile{WorkID: first, Kind: personalwork.FileKindArtifact, Name: "plan.pdf"}, false)
	require.ErrorIs(t, err, access.ErrAccessDenied)
	require.ErrorIs(t, works.DeleteFile(ctx, owner, personalwork.WorkFile{WorkID: first, Kind: personalwork.FileKindArtifact, Name: "plan.pdf"}), domain.ErrWorkFilesClosed)
}

// personalStartupCatalog publishes the core runtime plugin as a fixed release
// with a typed sensitive option. Agent still owns composition, validation,
// persistence and public redaction.
type personalStartupCatalog struct{ creationCatalog }

func (*personalStartupCatalog) ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error) {
	return []domain.PersonalPlugin{{Slug: "talgent-runtime", Enabled: true}}, nil
}

func (*personalStartupCatalog) ResolvePlugin(_ context.Context, _ string, slug string) (domain.PluginRelease, error) {
	return domain.PluginRelease{Slug: slug, Source: "owned", Version: "1", RetrievalRef: "archive:runtime-1", NativeEntry: []byte(`{"name":"talgent-runtime","version":"1"}`), Agents: []domain.AgentComponent{{Name: "talgent-runtime:personal-work-executor", Description: "Personal Work executor"}}, Parameters: pluginconfig.ParameterDeclarations{"token": {Type: "string", Title: "Token", Description: "Fixture token", Sensitive: true}}}, nil
}

func (*personalStartupCatalog) ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error) {
	return nil, nil
}

func (*personalStartupCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}
