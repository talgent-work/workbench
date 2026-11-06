package command

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type CreateWork struct {
	RequestID string
	WorkModel domain.WorkModelSelection
	// InitialExecution is committed with a newly registered Session.
	InitialExecution *PrepareWorkExecution
	AgentID          string
	SubjectID        string
	// StartingUserID is the trusted represented human. Empty means an
	// automated start without personal defaults.
	StartingUserID string
	// Subject is the optional business object the Work is about; ProjectID
	// is its optional Project.
	Subject               domain.WorkSubject
	ProjectID             string
	WorkspaceReference    string
	InputSource           domain.WorkInputSource
	ReadOnlyMounts        []domain.ReadOnlyMount `json:",omitempty"`
	TraceAttributes       map[string]string
	CapabilityDifferences domain.CapabilityDifferences
	// StartingContext, when supplied, starts a new Work from another Work of
	// the same definition; a reused Work keeps its own.
	StartingContext *domain.StartingContextSource
	// WorkID, when supplied, is the identity the caller allocated; a Personal
	// Work's File Space is opened under it before the Work exists.
	WorkID string
	// DisplayTitle is a Personal Work's initial display title.
	DisplayTitle string
	// ComputerID is the Computer its starter chose; empty runs it on the
	// default Computer (see workComputer).
	ComputerID string
	// Harness is the starter's choice for the Work's Harness, admitted only
	// on a Managed Computer.
	Harness domain.HarnessChoice
}

// WorkRegistrar persists one new Session against the definition revision
// used for its creation. A competing registration or changed definition returns
// ErrConcurrentModification; the use case reloads and lets AgentDefinition locate it.
// When initial input is supplied, registration and its recovery receipt commit
// together, so a registered Session never loses its original startup input.
type WorkRegistration struct {
	SubjectID, RequestID string
	// WorkspaceReference is the one the creating caller named, part of its
	// creation correlation even for a Work that keeps no Session Workspace.
	WorkspaceReference string
	DefinitionVersion  int
	InitialExecution   *PrepareWorkExecution
}

type WorkRegistrar interface {
	RegisterWork(context.Context, *domain.AgentWork, WorkRegistration) error
}

type CreateWorkHandler struct {
	now           func() time.Time
	connections   PersonalConnectionsPort
	admissions    StartAdmissions
	computers     ComputerAdmissionPort
	execution     ExecutionRequestsFactory
	plugins       PluginCatalogPort
	models        query.ModelConfigurations
	agents        domain.AgentDefinitionRepository
	sessions      WorkRegistrar
	lookup        domain.WorkLookupPort
	authorization access.WorkAuthorizer
}

func NewCreateWorkHandler(now func() time.Time, agents domain.AgentDefinitionRepository, sessions WorkRegistrar, lookup domain.WorkLookupPort, authorization access.WorkAuthorizer, models query.ModelConfigurations, plugins PluginCatalogPort, connections PersonalConnectionsPort, admissions StartAdmissions, computers ComputerAdmissionPort, execution ExecutionRequestsFactory) *CreateWorkHandler {
	return &CreateWorkHandler{now: now, connections: connections, admissions: admissions, computers: computers, execution: execution, agents: agents, sessions: sessions, lookup: lookup, authorization: authorization, models: models, plugins: plugins}
}

func (h *CreateWorkHandler) Handle(ctx context.Context, request CreateWork) (string, error) {
	if request.InitialExecution != nil {
		if err := validateInitialPreparation(*request.InitialExecution); err != nil {
			return "", err
		}
	}
	if request.InitialExecution != nil {
		if request.RequestID != "" && request.RequestID != request.InitialExecution.RequestID {
			return "", ErrStartRequestConflict
		}
		request.RequestID = request.InitialExecution.RequestID
	}
	if uuid.Validate(request.RequestID) != nil {
		return "", query.ErrInvalidStartRequest
	}
	for range 8 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Read on every attempt: a competing creation of the same request
		// identity that won registration is found here and reused.
		accepted, admitted, err := h.admissions.FindAdmittedStart(ctx, request.RequestID)
		if err != nil {
			return "", err
		}
		if admitted && accepted.Kind != query.StartKindStart {
			return "", ErrStartRequestConflict
		}
		agent, err := h.agents.Get(ctx, request.AgentID)
		if err != nil {
			return "", err
		}
		if owner := agent.PersonalAuthorizationOwner(); request.StartingUserID != "" && owner != request.StartingUserID {
			return "", access.ErrAccessDenied
		}
		attributes := agent.WorkTraceAttributes(request.TraceAttributes)
		// Reuse precedes all personal/catalog reads. A retry of an existing binding
		// cannot absorb new defaults, releases, or capability choices.
		existing, err := h.reusableWork(ctx, agent, request.Subject, accepted, admitted)
		if err != nil {
			return "", err
		}
		if admitted && (existing == nil || existing.ID != accepted.WorkID) {
			return "", ErrStartRequestConflict
		}
		if existing != nil {
			if err := h.authorization.AuthorizeWork(ctx, request.SubjectID, existing.ID); err != nil {
				return "", err
			}
			return existing.ID, nil
		}
		// Personal capabilities and authorizations belong to the represented human.
		catalogUser := request.StartingUserID
		if catalogUser == "" {
			catalogUser = request.SubjectID
		}
		capabilities, err := resolveStartupCapabilities(ctx, h.plugins, catalogUser, request.StartingUserID, domain.StartupCapabilityInput{
			BasePrompt: agent.Capabilities.Prompt, RequiredPlugins: domain.WithRequiredPlugins(agent.Capabilities.RequiredPlugins, request.CapabilityDifferences.RequiredPlugins), DefaultStartupAgent: agent.Capabilities.StartupAgent,
			Prompt: request.CapabilityDifferences.Prompt, StartupAgent: request.CapabilityDifferences.StartupAgent, Plugins: request.CapabilityDifferences.Plugins,
			ConnectorChoices: request.CapabilityDifferences.Connectors,
		})
		if err != nil {
			return "", err
		}
		// Every authorization usable at creation is selected, independently of
		// Plugin and Connector choices.
		authorizationInput := domain.WorkAuthorizationInput{OwnerUserID: agent.PersonalAuthorizationOwner()}
		if authorizationInput.OwnerUserID != "" {
			authorizationInput.Available, err = h.connections.ListPersonalAuthorizations(ctx, authorizationInput.OwnerUserID)
			if err != nil {
				return "", err
			}
		}
		repositories, err := h.repositoryInput(ctx, agent.PersonalAuthorizationOwner(), request.InitialExecution)
		if err != nil {
			return "", err
		}
		// A named Model Configuration that is not provided is refused for that
		// reason, whatever the Computer, before Computer is asked.
		if named := request.WorkModel; named.ConfigurationID != "" {
			if _, provided := h.models.Option(named); !provided {
				return h.reuseAfterRejection(ctx, agent, request, domain.ErrModelConfigurationWithdrawn)
			}
		}
		computer, err := workComputer(ctx, h.computers, request.SubjectID, request.ComputerID)
		if err != nil {
			return h.reuseAfterRejection(ctx, agent, request, err)
		}
		managed := !computer.Cloud
		// A Computer the definition's Works may not run on is refused
		// before a run is seated there.
		if err := agent.AdmitComputer(managed); err != nil {
			return h.reuseAfterRejection(ctx, agent, request, err)
		}
		model := h.workModel(request.WorkModel, managed)
		if err := model.Validate(); err != nil {
			return "", err
		}
		harness, err := request.Harness.On(managed)
		if err != nil {
			return "", err
		}
		if err := model.AdmitOn(managed); err != nil {
			return "", err
		}
		_, provided := h.models.Option(model)
		available := model.Available(provided)
		_, thinking, _ := h.models.ThinkingLevels(model)
		// A configuration that is not provided is refused by admission with
		// that reason; its thinking levels are not consulted.
		if available && request.InitialExecution != nil {
			if err := validateInitialThinkingLevel(h.models, model, request.InitialExecution.ThinkingLevel); err != nil {
				return "", err
			}
			if request.InitialExecution.ThinkingLevel != "" {
				thinking = request.InitialExecution.ThinkingLevel
			}
		}
		// Definition and Computer admission are consulted only for an
		// available model; admission refuses a withdrawn Model Configuration
		// first.
		admission := domain.StartAdmission{AdmittedAt: h.now(), AgentID: agent.ID, ModelConfigurationProvided: provided, Soul: agent.Soul}
		workID := request.WorkID
		if workID == "" {
			workID = uuid.Must(uuid.NewV7()).String()
		}
		if available {
			seat, err := admitRun(ctx, h.computers, RunAdmission{ComputerID: computer.ID, WorkID: workID, WorkOwnerID: agent.WorkPrincipal(), StarterID: request.SubjectID, Sandbox: harness.Sandbox})
			if err != nil {
				return h.reuseAfterRejection(ctx, agent, request, err)
			}
			admission.DefinitionAdmitted, admission.Seat = true, seat
		}
		requests := h.execution()

		work, created, err := agent.CreateWork(ctx, domain.WorkCreationRequest{StartedBy: request.SubjectID, RepositoryInput: repositories, AuthorizationInput: authorizationInput, WorkModel: model, ComputerManaged: managed, Harness: harness, Subject: request.Subject, ProjectID: request.ProjectID, WorkspaceReference: request.WorkspaceReference, InputSource: request.InputSource, ReadOnlyMounts: request.ReadOnlyMounts, Capabilities: capabilities, TraceAttributes: attributes, ThinkingLevel: thinking, Admission: admission, StartingContext: request.StartingContext, WorkID: workID, DisplayTitle: request.DisplayTitle}, h.lookup, requests)
		if err != nil {
			h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
			return "", err
		}

		if !created {
			// Another creation established this Work; its run holds its own Desk.
			h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
			if err := h.authorization.AuthorizeWork(ctx, request.SubjectID, work.ID); err != nil {
				return "", err
			}
			return work.ID, nil
		}

		var initial *PrepareWorkExecution
		if request.InitialExecution != nil {
			retained := *request.InitialExecution
			retained.SubjectID = request.SubjectID
			retained.WorkID = work.ID
			retained.WorkspaceReference = request.WorkspaceReference
			if err := (InitialExecutionRequest{PrepareWorkExecution: retained}).AdmitRepositoryPreparation(work); err != nil {
				h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
				return "", err
			}
			initial = &retained
		}
		err = h.sessions.RegisterWork(ctx, work, WorkRegistration{SubjectID: request.SubjectID, RequestID: request.RequestID, WorkspaceReference: request.WorkspaceReference, DefinitionVersion: agent.Version, InitialExecution: initial})
		if err != nil {
			h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
		}
		if errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		if err != nil {
			return "", err
		}
		// Initial input preparation waits for its cross-context binding confirmation;
		// the Work already retains the admitted startup obligation for recovery.
		if initial == nil {
			return work.ID, requests.Send(ctx)
		}
		return work.ID, nil
	}
	return "", domain.ErrConcurrentModification
}

// workModel is the model a new Work retains: the requested one, or, with none
// requested, the machine's Claude login on a Managed Computer and the first
// provided Model Configuration elsewhere.
func (h *CreateWorkHandler) workModel(requested domain.WorkModelSelection, managed bool) domain.WorkModelSelection {
	if requested != (domain.WorkModelSelection{}) {
		return requested
	}
	if managed {
		return domain.MachineLoginModel()
	}
	if option, ok := h.models.Default(); ok {
		return option.Selection
	}
	return requested
}

// reusableWork is the Work a request returns instead of creating one: the
// definition's Work for its Subject, or, without a Subject, the Work its own
// request identity already started.
func (h *CreateWorkHandler) reusableWork(ctx context.Context, agent *domain.AgentDefinition, subject domain.WorkSubject, accepted query.AdmittedStart, admitted bool) (*domain.AgentWork, error) {
	if subject.Present() || !admitted || accepted.WorkID == "" {
		return agent.ReuseWork(ctx, subject, h.lookup)
	}
	return h.lookup.LocateWork(ctx, accepted.WorkID)
}

// A competing creation may establish this binding after our first lookup but
// before admission is refused. Reusing that Work needs no new capacity.
func (h *CreateWorkHandler) reuseAfterRejection(ctx context.Context, agent *domain.AgentDefinition, request CreateWork, rejection error) (string, error) {
	work, err := agent.ReuseWork(ctx, request.Subject, h.lookup)
	if err != nil {
		return "", err
	}
	if work == nil {
		return "", rejection
	}
	accepted, found, err := h.admissions.FindAdmittedStart(ctx, request.RequestID)
	if err != nil {
		return "", err
	}
	if found && (accepted.Kind != query.StartKindStart || accepted.WorkID != work.ID) {
		return "", ErrStartRequestConflict
	}
	if err := h.authorization.AuthorizeWork(ctx, request.SubjectID, work.ID); err != nil {
		return "", err
	}
	return work.ID, nil
}

func validateInitialThinkingLevel(models query.ModelConfigurations, model domain.WorkModelSelection, level string) error {
	if level == "" {
		return nil
	}
	if levels, _, ok := models.ThinkingLevels(model); ok && slices.Contains(levels, level) {
		return nil
	}
	return fmt.Errorf("%w: thinking level is not available for this Work model", domain.ErrInvalidWork)
}
