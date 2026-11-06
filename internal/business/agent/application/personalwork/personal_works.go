package personalwork

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// PersonalWorks serves a person's own Works under their employee
// definition, their files and sharing, and their Personal Library.
type PersonalWorks struct {
	app       *application.Application
	works     Works
	files     FileSpaces
	directory UserDirectory
}

func New(app *application.Application, works Works, files FileSpaces, directory UserDirectory) *PersonalWorks {
	return &PersonalWorks{app: app, works: works, files: files, directory: directory}
}

// Person is the authenticated person a request acts for. Agent, when set, is
// the Agent of one of that person's Works asking on their behalf.
type Person struct {
	UserID, DisplayName string
	Agent               *ActingAgent
}

// ActingAgent is the Work an Agent asks from: its Project, if any, and its
// definition's purpose.
type ActingAgent struct{ ProjectID, Purpose string }

// self is the person asking for themselves; an Agent asks for no one here.
func (p Person) self() string {
	if p.Agent != nil {
		return ""
	}
	return p.UserID
}

// libraryReader is whom a Library read is for: the person asking for
// themselves, or the owner of the Personal Work whose Agent asks. The Agent
// of any other Work reads no one's Library.
func (p Person) libraryReader() string {
	if p.Agent != nil && (p.Agent.ProjectID != "" || p.Agent.Purpose != domain.EmployeeWorkPurpose) {
		return ""
	}
	return p.UserID
}

// StartOptions lists what a new Personal Work may start with.
func (p *PersonalWorks) StartOptions(ctx context.Context, person Person) (query.WorkStartOptions, error) {
	if person.self() == "" {
		return query.WorkStartOptions{}, access.ErrAccessDenied
	}
	return p.app.GetWorkStartOptions(ctx, query.WorkStartOptionsQuery{SubjectID: person.UserID, EmployeeID: person.UserID})
}

// StartRepositories lists the repositories the person may check out.
func (p *PersonalWorks) StartRepositories(ctx context.Context, person Person) (query.WorkStartRepositories, error) {
	if person.self() == "" {
		return query.WorkStartRepositories{}, access.ErrAccessDenied
	}
	return p.app.ListWorkStartRepositories(ctx, person.UserID)
}

// StartPersonalWork is a new Personal Work and its first input.
type StartPersonalWork struct {
	Person         Person
	RequestID      string
	InitialMessage string
	Selections     []command.RepositorySelection
	Capabilities   domain.CapabilityDifferences
	WorkModel      domain.WorkModelSelection
	ThinkingLevel  string
	// ComputerID is the Computer the person chose; empty runs it on the
	// default Computer, as command.CreateWork.ComputerID describes.
	ComputerID string
	// Harness is the person's choice for the Work's Harness, admitted only
	// on a Managed Computer.
	Harness domain.HarnessChoice
}

// personalWorkSpace derives a Personal Work's identity, and with it its File
// Space's owner, from the person and the request identity: a repeated
// request opens the same space and names the same Work.
var personalWorkSpace = uuid.MustParse("6f5c1c8e-9a0b-4c7e-8a51-3d2f0b9e4a17")

// Start opens the Work's File Space under the identity Agent allocates for
// it, then creates the Work with that space as its input source and admits
// its first input. The Work has no Subject, no Project and no read-only
// mount; only its creation request identity deduplicates it.
func (p *PersonalWorks) Start(ctx context.Context, input StartPersonalWork) (string, error) {
	if input.Person.UserID == "" {
		return "", access.ErrAccessDenied
	}
	if uuid.Validate(input.RequestID) != nil || strings.TrimSpace(input.InitialMessage) == "" {
		return "", ErrInvalidRequest
	}
	definition, err := p.app.EnsureEmployeeAgent(ctx, input.Person.UserID, input.Person.DisplayName)
	if err != nil {
		return "", err
	}
	workID := uuid.NewSHA1(personalWorkSpace, []byte(input.Person.UserID+"/"+input.RequestID)).String()
	source, err := p.files.OpenFileSpace(ctx, workID)
	if err != nil {
		return "", err
	}
	initial := command.PrepareWorkExecution{
		SubjectID: input.Person.UserID, RequestID: input.RequestID, ThinkingLevel: input.ThinkingLevel,
		WorkBranch: "talgent/" + workID[:8], Input: command.ExecutionInput{ID: input.RequestID, Text: input.InitialMessage}, Selections: input.Selections,
	}
	return p.app.Commands.CreateRequestedWork.Handle(ctx, command.CreateWork{
		StartingUserID: input.Person.UserID, SubjectID: input.Person.UserID, RequestID: input.RequestID, AgentID: definition.AgentID,
		WorkID: workID, DisplayTitle: domain.DisplayTitleFrom(input.InitialMessage),
		WorkspaceReference: uuid.NewSHA1(personalWorkSpace, []byte(workID+"/workspace")).String(), InputSource: source,
		WorkModel: input.WorkModel, CapabilityDifferences: input.Capabilities, InitialExecution: &initial, ComputerID: input.ComputerID, Harness: input.Harness,
	})
}

const (
	defaultListedWorks = 50
	maxListedWorks     = 100
)

// List lists the person's Personal Works, closed ones included, most
// recently updated first; a person without a definition yet has none.
func (p *PersonalWorks) List(ctx context.Context, person Person, limit int) ([]PersonalWorkSummary, error) {
	agentID, found, err := p.employeeDefinition(ctx, person)
	if err != nil || !found {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultListedWorks
	}
	return p.works.ListPersonalWorks(ctx, agentID, min(limit, maxListedWorks))
}

// employeeDefinition locates the person's employee definition without
// establishing it.
func (p *PersonalWorks) employeeDefinition(ctx context.Context, person Person) (string, bool, error) {
	if person.self() == "" {
		return "", false, access.ErrAccessDenied
	}
	return p.app.FindEmployeeAgent(ctx, person.UserID)
}

// ChangeTitle replaces the owner's Personal Work's display title.
func (p *PersonalWorks) ChangeTitle(ctx context.Context, person Person, workID, title string) error {
	if person.self() == "" {
		return access.ErrAccessDenied
	}
	return p.app.ChangeDisplayTitle(ctx, application.ChangeDisplayTitle{ActorUserID: person.UserID, WorkID: strings.TrimSpace(workID), Title: title})
}

const (
	defaultDirectoryResults = 10
	maxDirectoryResults     = 20
	maxDirectoryLookup      = 100
)

// SearchShareReaders finds people in the User Directory.
func (p *PersonalWorks) SearchShareReaders(ctx context.Context, person Person, search string, limit int) ([]DirectoryUser, error) {
	if person.self() == "" {
		return nil, access.ErrAccessDenied
	}
	if strings.TrimSpace(search) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultDirectoryResults
	}
	return p.directory.SearchUsers(ctx, strings.TrimSpace(search), min(limit, maxDirectoryResults))
}

// GetShareReaders shows people in the User Directory by identity.
func (p *PersonalWorks) GetShareReaders(ctx context.Context, person Person, userIDs []string) ([]DirectoryUser, error) {
	if person.self() == "" {
		return nil, access.ErrAccessDenied
	}
	if len(userIDs) > maxDirectoryLookup {
		return nil, ErrInvalidRequest
	}
	if len(userIDs) == 0 {
		return nil, nil
	}
	return p.directory.GetUsers(ctx, userIDs)
}

// GrantShare lets one person in the User Directory read the owner's
// Personal Work: a deployment is one enterprise, so the directory is the
// scope of a Work without a Project.
func (p *PersonalWorks) GrantShare(ctx context.Context, person Person, workID, reader string) error {
	if person.self() == "" {
		return access.ErrAccessDenied
	}
	reader = strings.TrimSpace(reader)
	if reader == "" {
		return domain.ErrInvalidVisibility
	}
	known, err := p.directory.GetUsers(ctx, []string{reader})
	if err != nil {
		return err
	}
	if len(known) == 0 || known[0].UserID != reader {
		return ErrReaderUnknown
	}
	return p.app.GrantWorkShare(ctx, application.GrantWorkShare{RequesterID: person.UserID, WorkID: strings.TrimSpace(workID), ReaderID: reader, Admission: domain.ReaderAdmission{Directory: true}})
}
