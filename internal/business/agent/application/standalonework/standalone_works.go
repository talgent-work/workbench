package standalonework

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// StandaloneWorks serves an owner's own Works under its definition, a
// person's or an Application's, a person's files and sharing, and their
// Personal Library.
type StandaloneWorks struct {
	app      *application.Application
	works    query.WorkQueryRepository
	files    command.StoragePort
	accounts command.AccountPort
}

func New(app *application.Application, works query.WorkQueryRepository, files command.StoragePort, accounts command.AccountPort) *StandaloneWorks {
	return &StandaloneWorks{app: app, works: works, files: files, accounts: accounts}
}

// StartOptions lists what a new Standalone Work may start with under the
// owner's definition.
func (p *StandaloneWorks) StartOptions(ctx context.Context, caller Caller) (query.WorkStartOptions, error) {
	owner := caller.Owner()
	if owner == "" {
		return query.WorkStartOptions{}, access.ErrAccessDenied
	}
	return p.app.GetWorkStartOptions(ctx, query.WorkStartOptionsQuery{SubjectID: owner, EmployeeID: caller.self(), ApplicationID: caller.ApplicationID})
}

// StartRepositories lists the repositories the person may check out.
func (p *StandaloneWorks) StartRepositories(ctx context.Context, caller Caller) (query.WorkStartRepositories, error) {
	if caller.self() == "" {
		return query.WorkStartRepositories{}, access.ErrAccessDenied
	}
	return p.app.ListWorkStartRepositories(ctx, caller.UserID)
}

// StartWork is a new Standalone Work and its first input.
type StartWork struct {
	Caller         Caller
	RequestID      string
	InitialMessage string
	Selections     []command.RepositorySelection
	Capabilities   domain.CapabilityDifferences
	WorkModel      domain.WorkModelSelection
	ThinkingLevel  string
	// ComputerID is the Computer the owner chose; empty runs it on the
	// default Computer, as command.CreateWork.ComputerID describes.
	ComputerID string
	// Harness is the owner's choice for the Work's Harness, admitted only on
	// a Managed Computer.
	Harness domain.HarnessChoice
	// Labels are the Work Labels the owner gives the Work, kept for its life.
	Labels map[string]string
}

// standaloneWorkSpace derives a Standalone Work's identity, and with it the owner
// of its File Spaces, from its owner and the request identity: a repeated
// request names the same Work.
var standaloneWorkSpace = uuid.MustParse("6f5c1c8e-9a0b-4c7e-8a51-3d2f0b9e4a17")

// Start creates the Work under the identity Agent allocates for it and admits
// its first input. The Work has no Subject and no Project, so its layout
// locates its own inputs and publication target; only its creation request
// identity deduplicates it. A person starts it for themselves; an
// Application starts it as itself, with no person starting it.
func (p *StandaloneWorks) Start(ctx context.Context, input StartWork) (string, error) {
	owner := input.Caller.Owner()
	if owner == "" {
		return "", access.ErrAccessDenied
	}
	if uuid.Validate(input.RequestID) != nil || strings.TrimSpace(input.InitialMessage) == "" {
		return "", ErrInvalidRequest
	}
	labels, err := domain.NewWorkLabels(input.Labels)
	if err != nil {
		return "", err
	}
	definition, err := p.ensureDefinition(ctx, input.Caller)
	if err != nil {
		return "", err
	}
	workID := uuid.NewSHA1(standaloneWorkSpace, []byte(owner+"/"+input.RequestID)).String()
	initial := command.PrepareWorkExecution{
		SubjectID: owner, RequestID: input.RequestID, ThinkingLevel: input.ThinkingLevel,
		WorkBranch: "talgent/" + workID[:8], Input: command.ExecutionInput{ID: input.RequestID, Text: input.InitialMessage}, Selections: input.Selections,
	}
	return p.app.Commands.CreateWork.Handle(ctx, command.CreateWork{
		StartingUserID: input.Caller.self(), SubjectID: owner, RequestID: input.RequestID, AgentID: definition.AgentID,
		WorkID: workID, DisplayTitle: domain.DisplayTitleFrom(input.InitialMessage),
		WorkModel: input.WorkModel, CapabilityDifferences: input.Capabilities, InitialExecution: &initial, ComputerID: input.ComputerID, Harness: input.Harness, WorkLabels: labels,
	})
}

// ensureDefinition returns the owner's definition, establishing it on its
// first need: a person's from their trusted identity, an Application's from
// its name in Account. A removed or unknown Application starts nothing.
func (p *StandaloneWorks) ensureDefinition(ctx context.Context, caller Caller) (command.InitializedAgentDefinition, error) {
	if caller.ApplicationID == "" {
		return p.app.EnsureEmployeeAgent(ctx, caller.UserID, caller.DisplayName)
	}
	return p.app.EnsureOwnApplicationAgent(ctx, caller.ApplicationID)
}

const (
	defaultListedWorks = 50
	maxListedWorks     = 100
)

// ListWorks narrows the owner's Standalone Works: to those carrying every one
// of Labels and, when States is not empty, to those in one of them. At most
// Limit are listed; zero lists the default number.
type ListWorks struct {
	Caller Caller
	Limit  int
	Labels map[string]string
	States []string
}

// List lists the owner's Standalone Works, closed ones included, most
// recently updated first; an owner without a definition yet has none.
func (p *StandaloneWorks) List(ctx context.Context, input ListWorks) ([]query.StandaloneWorkSummary, error) {
	labels, err := domain.NewWorkLabels(input.Labels)
	if err != nil {
		return nil, err
	}
	states, err := listedStates(input.States)
	if err != nil {
		return nil, err
	}
	agentID, found, err := p.ownDefinition(ctx, input.Caller)
	if err != nil || !found {
		return nil, err
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultListedWorks
	}
	return p.works.ListStandaloneWorks(ctx, query.StandaloneWorksFilter{AgentID: agentID, Labels: labels.Pairs(), States: states, Limit: min(limit, maxListedWorks)})
}

// listedStates are the Work States a listing asks for, as Works show them.
func listedStates(requested []string) ([]domain.WorkState, error) {
	states := make([]domain.WorkState, 0, len(requested))
	for _, state := range requested {
		if !slices.Contains(domain.WorkStates, domain.WorkState(state)) {
			return nil, fmt.Errorf("%w: unknown work state %q", ErrInvalidRequest, state)
		}
		states = append(states, domain.WorkState(state))
	}
	return states, nil
}

// ownDefinition locates the owner's definition without establishing it.
func (p *StandaloneWorks) ownDefinition(ctx context.Context, caller Caller) (string, bool, error) {
	switch {
	case caller.Owner() == "":
		return "", false, access.ErrAccessDenied
	case caller.ApplicationID != "":
		return p.app.FindApplicationAgent(ctx, caller.ApplicationID)
	default:
		return p.app.FindEmployeeAgent(ctx, caller.UserID)
	}
}

// ChangeTitle replaces the owner's Standalone Work's display title.
func (p *StandaloneWorks) ChangeTitle(ctx context.Context, caller Caller, workID, title string) error {
	owner := caller.Owner()
	if owner == "" {
		return access.ErrAccessDenied
	}
	return p.app.ChangeDisplayTitle(ctx, application.ChangeDisplayTitle{ActorID: owner, WorkID: strings.TrimSpace(workID), Title: title})
}

const (
	defaultDirectoryResults = 10
	maxDirectoryResults     = 20
	maxDirectoryLookup      = 100
)

// SearchShareReaders finds people in the User Directory.
func (p *StandaloneWorks) SearchShareReaders(ctx context.Context, caller Caller, search string, limit int) ([]command.DirectoryUser, error) {
	if caller.self() == "" {
		return nil, access.ErrAccessDenied
	}
	if strings.TrimSpace(search) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultDirectoryResults
	}
	return p.accounts.SearchUsers(ctx, strings.TrimSpace(search), min(limit, maxDirectoryResults))
}

// GetShareReaders shows people in the User Directory by identity.
func (p *StandaloneWorks) GetShareReaders(ctx context.Context, caller Caller, userIDs []string) ([]command.DirectoryUser, error) {
	if caller.self() == "" {
		return nil, access.ErrAccessDenied
	}
	if len(userIDs) > maxDirectoryLookup {
		return nil, ErrInvalidRequest
	}
	if len(userIDs) == 0 {
		return nil, nil
	}
	return p.accounts.GetUsers(ctx, userIDs)
}

// GrantShare lets one person in the User Directory read the owner's
// Standalone Work: a deployment is one enterprise, so the directory is the
// scope of a Work without a Project.
func (p *StandaloneWorks) GrantShare(ctx context.Context, caller Caller, workID, reader string) error {
	if caller.self() == "" {
		return access.ErrAccessDenied
	}
	reader = strings.TrimSpace(reader)
	if reader == "" {
		return domain.ErrInvalidVisibility
	}
	known, err := p.accounts.GetUsers(ctx, []string{reader})
	if err != nil {
		return err
	}
	if len(known) == 0 || known[0].UserID != reader {
		return ErrReaderUnknown
	}
	return p.app.GrantWorkShare(ctx, application.GrantWorkShare{RequesterID: caller.UserID, WorkID: strings.TrimSpace(workID), ReaderID: reader, Admission: domain.ReaderAdmission{Directory: true}})
}
