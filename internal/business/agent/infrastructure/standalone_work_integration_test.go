//go:build integration

package infrastructure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/standalonework"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// librarySpaces stands in for Storage's Directories: every Directory opens
// with a token naming it, and the files are the fixture's.
type librarySpaces struct {
	command.StoragePort
	files  []command.SpaceFile
	issued []string
}

// directoryName names a Directory by its owner facts and name.
func directoryName(directory domain.Directory) string {
	name := ""
	for _, fact := range directory.Owner {
		name += string(fact.Condition) + ":" + fact.Value + "/"
	}
	return name + directory.Name
}

func (s *librarySpaces) OpenDirectory(_ context.Context, located domain.LocatedDirectory) (string, error) {
	return "token-" + directoryName(located.Directory), nil
}

func (s *librarySpaces) filesOf(directories ...domain.Directory) []command.SpaceFile {
	var out []command.SpaceFile
	for _, directory := range directories {
		for _, file := range s.files {
			if file.Directory.Equal(directory) {
				out = append(out, file)
			}
		}
	}
	return out
}

func (s *librarySpaces) ListFiles(_ context.Context, directory domain.Directory) ([]command.SpaceFile, error) {
	return s.filesOf(directory), nil
}

func (s *librarySpaces) ListDirectoryFiles(_ context.Context, directories []domain.Directory) ([]command.SpaceFile, error) {
	return s.filesOf(directories...), nil
}

func (s *librarySpaces) IssueAccessLink(_ context.Context, position command.FilePosition, _ bool) (command.FileLink, error) {
	s.issued = append(s.issued, directoryName(position.Directory)+"/"+position.Name)
	return command.FileLink{URL: "https://files.invalid/" + position.Name}, nil
}

func (s *librarySpaces) DeleteFile(context.Context, command.FilePosition, string, command.FileOwner) error {
	return nil
}

func workFiles(workID, name string) domain.Directory {
	return domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionWork, Value: workID}}, Name: name}
}

func caseFiles(caseID, name string) domain.Directory {
	return domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionSubject, Value: "case:" + caseID}}, Name: name}
}

type knownPeople map[string]command.DirectoryUser

func (k knownPeople) SearchUsers(context.Context, string, int) ([]command.DirectoryUser, error) {
	return nil, nil
}

func (k knownPeople) GetUsers(_ context.Context, ids []string) ([]command.DirectoryUser, error) {
	var out []command.DirectoryUser
	for _, id := range ids {
		if user, ok := k[id]; ok {
			out = append(out, user)
		}
	}
	return out, nil
}

func (knownPeople) IssueWorkIdentityToken(context.Context, command.WorkIdentityFacts) (command.IdentityToken, error) {
	return command.IdentityToken{}, errors.New("personal Works issue no token here")
}

func (knownPeople) GetApplication(context.Context, string) (command.OwnedApplication, error) {
	return command.OwnedApplication{}, command.ErrApplicationUnknown
}

func standaloneWorksFixture(t *testing.T, db *xorm.Engine, spaces *librarySpaces) (*standalonework.StandaloneWorks, *application.Application) {
	t.Helper()
	options := agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})
	create := creationCoordinator(db, startupAuthority{}, nil, options, &personalStartupCatalog{})
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	lookup := infrastructure.NewAgentDefinitionQueryRepository(db)
	app := &application.Application{Definitions: definitions, DefinitionQueries: lookup, Works: infrastructure.NewAgentWorkRepository(db, time.Now), WorkQueries: infrastructure.NewWorkQueryRepository(db), Commands: application.Commands{
		EnsureAgentDefinition: command.NewEnsureAgentDefinitionHandler(definitions, lookup),
		CreateWork:            create,
	}}
	directory := knownPeople{"reader": {UserID: "reader", Username: "reader"}}
	return standalonework.New(app, infrastructure.NewWorkQueryRepository(db), spaces, directory), app
}

// A person's Standalone Works start under the definition Agent establishes for
// them, each with its own File Space; their list, sharing, Library and file
// access follow from the owner and Work Shares alone.
func TestStandaloneWorksStartListShareAndFillTheLibrary(t *testing.T) {
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	spaces := &librarySpaces{}
	works, app := standaloneWorksFixture(t, db, spaces)
	owner := standalonework.Caller{UserID: "owner", DisplayName: "Owner"}
	reader := standalonework.Caller{UserID: "reader"}
	stranger := standalonework.Caller{UserID: "stranger"}

	_, err = works.Start(ctx, standalonework.StartWork{Caller: owner, RequestID: "not-a-uuid", InitialMessage: "x"})
	require.ErrorIs(t, err, standalonework.ErrInvalidRequest)
	empty, err := works.List(ctx, owner, 0)
	require.NoError(t, err)
	require.Empty(t, empty, "a person without a definition has no Standalone Works")

	request := uuid.NewString()
	first, err := works.Start(ctx, standalonework.StartWork{Caller: owner, RequestID: request, InitialMessage: "Draft the plan\nwith details"})
	require.NoError(t, err)
	again, err := works.Start(ctx, standalonework.StartWork{Caller: owner, RequestID: request, InitialMessage: "Draft the plan\nwith details"})
	require.NoError(t, err)
	require.Equal(t, first, again, "a repeated request returns the Work it started")
	second, err := works.Start(ctx, standalonework.StartWork{Caller: owner, RequestID: uuid.NewString(), InitialMessage: "Summarize the notes"})
	require.NoError(t, err)

	stored, err := app.Works.Get(ctx, first)
	require.NoError(t, err)
	require.True(t, stored.Standalone())
	require.Equal(t, "Draft the plan", stored.DisplayTitle)
	require.Equal(t, workFiles(first, "inputs"), stored.LocatedLayout.Inputs.Directory, "a Standalone Work's inputs are its own")
	require.Equal(t, workFiles(first, "artifacts"), stored.LocatedLayout.PublicationTarget.Directory, "and so are its Artifacts")

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

	require.ErrorIs(t, works.GrantShare(ctx, owner, first, "ghost"), standalonework.ErrReaderUnknown)
	require.NoError(t, works.GrantShare(ctx, owner, first, "reader"))

	// A Work with a Subject and a Project, which publishes into its Subject's
	// Artifacts with the Subject's other Works, shared with the reader within
	// its Project.
	definition, err := app.EnsureEmployeeAgent(ctx, "owner", "Owner")
	require.NoError(t, err)
	project := uuid.NewString()
	initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), WorkBranch: "talgent/TAL-1", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Build it"}}
	subjectWork, err := app.Commands.CreateWork.Handle(ctx, command.CreateWork{StartingUserID: "owner", SubjectID: "owner", RequestID: initial.RequestID, AgentID: definition.AgentID,
		Subject: domain.WorkSubject{Kind: "case", ID: "case-x"}, ProjectID: project,
		InitialExecution: &initial})
	require.NoError(t, err)
	require.NoError(t, app.GrantWorkShare(ctx, application.GrantWorkShare{RequesterID: "owner", WorkID: subjectWork, ReaderID: "reader", Admission: domain.ReaderAdmission{ProjectID: project}}))

	now := time.Now().UTC()
	spaces.files = []command.SpaceFile{
		{Directory: workFiles(first, "inputs"), Kind: command.FileKindAttachment, Name: "brief.md", UpdatedAt: now},
		{Directory: workFiles(first, "artifacts"), Kind: command.FileKindArtifact, Name: "plan.pdf", WorkID: first, UpdatedAt: now.Add(-time.Hour)},
		{Directory: workFiles(second, "artifacts"), Kind: command.FileKindArtifact, Name: "notes.md", WorkID: second, UpdatedAt: now},
		{Directory: caseFiles("case-x", "artifacts"), Kind: command.FileKindArtifact, Name: "design.md", WorkID: subjectWork, UpdatedAt: now.Add(-2 * time.Hour)},
		{Directory: caseFiles("case-x", "artifacts"), Kind: command.FileKindArtifact, Name: "other.md", WorkID: uuid.NewString(), UpdatedAt: now},
		{Directory: caseFiles("case-x", "inputs"), Kind: command.FileKindAttachment, Name: "spec.md", UpdatedAt: now},
	}

	names := func(library standalonework.Library) []string {
		var out []string
		for _, file := range library.Files {
			out = append(out, file.File.Name)
		}
		return out
	}
	mine, err := works.Library(ctx, owner, standalonework.LibraryFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"notes.md", "plan.pdf"}, names(mine), "the owner's Library holds their Standalone Works' Artifacts, newest first")
	shared, err := works.Library(ctx, reader, standalonework.LibraryFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"plan.pdf", "design.md"}, names(shared), "a shared Work contributes only the Artifacts it published")
	for _, file := range shared.Files {
		require.Equal(t, "owner", file.Work.SharedBy)
	}
	narrowed, err := works.Library(ctx, reader, standalonework.LibraryFilter{NameQuery: "DESIGN"})
	require.NoError(t, err)
	require.Equal(t, []string{"design.md"}, names(narrowed))
	narrowed, err = works.Library(ctx, reader, standalonework.LibraryFilter{WorkID: first, AddedBefore: now})
	require.NoError(t, err)
	require.Equal(t, []string{"plan.pdf"}, names(narrowed))
	none, err := works.Library(ctx, stranger, standalonework.LibraryFilter{})
	require.NoError(t, err)
	require.Empty(t, none.Files)

	_, err = works.IssueAccessLink(ctx, reader, standalonework.WorkFile{WorkID: first, Kind: command.FileKindAttachment, Name: "brief.md"}, false)
	require.NoError(t, err, "a Work Share holder reads a Standalone Work's Attachments")
	_, err = works.IssueAccessLink(ctx, reader, standalonework.WorkFile{WorkID: subjectWork, Kind: command.FileKindArtifact, Name: "design.md"}, false)
	require.NoError(t, err)
	require.Equal(t, []string{"work:" + first + "/inputs/brief.md", "subject:case:case-x/artifacts/design.md"}, spaces.issued)
	for _, refused := range []standalonework.WorkFile{
		{WorkID: subjectWork, Kind: command.FileKindAttachment, Name: "spec.md"},
		{WorkID: subjectWork, Kind: command.FileKindArtifact, Name: "other.md"},
	} {
		_, err = works.IssueAccessLink(ctx, reader, refused, false)
		require.ErrorIs(t, err, command.ErrFileNotFound, refused.Name)
	}
	_, err = works.IssueAccessLink(ctx, stranger, standalonework.WorkFile{WorkID: first, Kind: command.FileKindArtifact, Name: "plan.pdf"}, false)
	require.ErrorIs(t, err, access.ErrAccessDenied)

	require.ErrorIs(t, works.DeleteFile(ctx, reader, standalonework.WorkFile{WorkID: first, Kind: command.FileKindAttachment, Name: "brief.md"}), access.ErrAccessDenied)
	require.NoError(t, works.DeleteFile(ctx, owner, standalonework.WorkFile{WorkID: first, Kind: command.FileKindAttachment, Name: "brief.md"}))
	require.ErrorIs(t, works.DeleteFile(ctx, owner, standalonework.WorkFile{WorkID: subjectWork, Kind: command.FileKindArtifact, Name: "design.md"}), domain.ErrWorkFilesUnavailable)

	// A Closed Work's Artifacts stay in the Library; a revoked Work Share
	// takes its Work's files out of the reader's.
	_, err = db.Exec("UPDATE agent_session SET state=7 WHERE id=?", first)
	require.NoError(t, err)
	mine, err = works.Library(ctx, owner, standalonework.LibraryFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"notes.md", "plan.pdf"}, names(mine))
	_, err = app.RevokeWorkShare(ctx, application.RevokeWorkShare{ActorID: "owner", WorkID: first, ReaderID: "reader"})
	require.NoError(t, err)
	shared, err = works.Library(ctx, reader, standalonework.LibraryFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"design.md"}, names(shared))
	_, err = works.IssueAccessLink(ctx, reader, standalonework.WorkFile{WorkID: first, Kind: command.FileKindArtifact, Name: "plan.pdf"}, false)
	require.ErrorIs(t, err, access.ErrAccessDenied)
	require.ErrorIs(t, works.DeleteFile(ctx, owner, standalonework.WorkFile{WorkID: first, Kind: command.FileKindArtifact, Name: "plan.pdf"}), domain.ErrWorkFilesClosed)
}

// personalStartupCatalog publishes the core plugin as a fixed release
// with a typed sensitive option. Agent still owns composition, validation,
// persistence and public redaction.
type personalStartupCatalog struct{ creationCatalog }

func (*personalStartupCatalog) ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error) {
	return []domain.PersonalPlugin{{Slug: "talgent", Enabled: true}}, nil
}

func (*personalStartupCatalog) ResolvePlugin(_ context.Context, _ string, slug string) (domain.PluginRelease, error) {
	return domain.PluginRelease{CatalogName: "talgent", Slug: slug, Source: "owned", Version: "1", RetrievalRef: "archive:runtime-1", NativeEntry: []byte(`{"name":"talgent","version":"1"}`), Agents: []domain.AgentComponent{{Name: "talgent:personal-work-executor", Description: "Personal Work executor"}}, Parameters: pluginconfig.ParameterDeclarations{"token": {Type: "string", Title: "Token", Description: "Fixture token", Sensitive: true}}}, nil
}

func (*personalStartupCatalog) ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error) {
	return nil, nil
}

func (*personalStartupCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}
