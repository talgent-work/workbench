package command

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// create makes one attempt to establish the request's Work: it reuses the
// definition's Work for the request's Subject, or derives a new Work and its
// Desk from the request identity, asks the Computer for admission and
// commits the Work in Starting with its request record. A failed commit
// leaves the Desk to a repeat, which asks for the same Desk, and otherwise
// to the open-Desk reconciliation; a known refusal releases it at once.
func (h *CreateWorkHandler) create(ctx context.Context, request CreateWork, fingerprint string) (string, error) {
	agent, err := h.agents.Get(ctx, request.AgentID)
	if err != nil {
		return "", err
	}
	if owner := agent.PersonalAuthorizationOwner(); request.StartingUserID != "" && owner != request.StartingUserID {
		return "", access.ErrAccessDenied
	}
	// Reuse precedes all personal and catalog reads: reusing an existing Work
	// cannot absorb new defaults, releases or capability choices.
	if existing, err := agent.ReuseWork(ctx, request.Subject, h.lookup); err != nil || existing != nil {
		if err != nil {
			return "", err
		}
		return h.reused(ctx, request, fingerprint, existing.ID)
	}
	// Personal capabilities and authorizations belong to the represented human.
	catalogUser := request.StartingUserID
	if catalogUser == "" {
		catalogUser = request.SubjectID
	}
	capabilities, err := resolveStartupCapabilities(ctx, h.catalog, catalogUser, request.StartingUserID, domain.StartupCapabilityInput{
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
		authorizationInput.Available, err = h.catalog.ListPersonalAuthorizations(ctx, authorizationInput.OwnerUserID)
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
			return h.reuseAfterRejection(ctx, agent, request, fingerprint, domain.ErrModelConfigurationWithdrawn)
		}
	}
	computer, err := workComputer(ctx, h.computers, request.SubjectID, request.ComputerID)
	if err != nil {
		return h.reuseAfterRejection(ctx, agent, request, fingerprint, err)
	}
	managed := !computer.Cloud
	// A Computer the definition's Works may not run on is refused before a
	// run is seated there.
	if err := agent.AdmitComputer(managed); err != nil {
		return h.reuseAfterRejection(ctx, agent, request, fingerprint, err)
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
	var initialInput *domain.InitialTaskInput
	if initial := request.InitialExecution; initial != nil {
		input, err := domain.NewInitialTaskInput(initial.Input.ID, initial.Input.Text)
		if err != nil {
			return "", err
		}
		initialInput = &input
		// A configuration that is not provided is refused by admission with
		// that reason; its thinking levels are not consulted.
		if available {
			if err := validateInitialThinkingLevel(h.models, model, initial.ThinkingLevel); err != nil {
				return "", err
			}
			if initial.ThinkingLevel != "" {
				thinking = initial.ThinkingLevel
			}
		}
	}
	// Definition and Computer admission are consulted only for an available
	// model; admission refuses a withdrawn Model Configuration first.
	admission := domain.StartAdmission{AdmittedAt: h.now(), AgentID: agent.ID, ModelConfigurationProvided: provided, Soul: agent.Soul}
	workID := request.WorkID
	if workID == "" {
		workID = requestedWorkID(request.RequestID)
	}
	if available {
		seat, err := admitRun(ctx, h.computers, RunAdmission{ComputerID: computer.ID, DeskID: requestedDeskID(request.RequestID), WorkID: workID, WorkOwnerID: agent.WorkPrincipal(), StarterID: request.SubjectID, Sandbox: harness.Sandbox})
		if err != nil {
			return h.reuseAfterRejection(ctx, agent, request, fingerprint, err)
		}
		admission.DefinitionAdmitted, admission.Seat = true, seat
	}
	requests := deferExecution(h.execution)
	work, established, err := agent.CreateWork(ctx, domain.WorkCreationRequest{StartedBy: request.SubjectID, RepositoryInput: repositories, AuthorizationInput: authorizationInput, WorkModel: model, ComputerManaged: managed, Harness: harness, Subject: request.Subject, ProjectID: request.ProjectID, WorkspaceReference: request.WorkspaceReference, InputSource: request.InputSource, ReadOnlyMounts: request.ReadOnlyMounts, Capabilities: capabilities, TraceAttributes: agent.WorkTraceAttributes(request.TraceAttributes), ThinkingLevel: thinking, Admission: admission, StartingContext: request.StartingContext, WorkID: workID, DisplayTitle: request.DisplayTitle, InitialTaskInput: initialInput}, h.lookup, requests)
	if err != nil {
		h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
		return "", err
	}
	if !established {
		// Another creation established the Work for this Subject; its run
		// holds its own Desk.
		h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
		return h.reused(ctx, request, fingerprint, work.ID)
	}
	if request.InitialExecution != nil {
		retained := *request.InitialExecution
		retained.WorkID = work.ID
		if err := (InitialExecutionRequest{PrepareWorkExecution: retained}).AdmitRepositoryPreparation(work); err != nil {
			h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
			return "", err
		}
	}
	record := query.StartRequest{ID: request.RequestID, Kind: query.StartKindStart, WorkID: work.ID, Fingerprint: fingerprint}
	err = h.sessions.RegisterWork(ctx, work, WorkRegistration{SubjectID: request.SubjectID, Request: record, WorkspaceReference: request.WorkspaceReference, DefinitionVersion: agent.Version, InitialExecution: initialPreparation(request, work.ID)})
	if errors.Is(err, ErrStartRequestCanceled) || errors.Is(err, ErrStartRequestConflict) {
		// The request can never create this Work: its Desk is not needed.
		h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
		return "", err
	}
	if err != nil {
		return "", err
	}
	if request.InitialExecution == nil {
		return work.ID, requests.send(ctx)
	}
	// The first start's execution follows its repository preparation.
	return h.created(ctx, request, work.ID)
}

// initialPreparation is the first start's preparation a new Work retains.
// It still carries a copy of the initial task input, which the startup view
// and a repeated preparation compare against; the Work's InitialTaskInput
// alone decides the hand-off. The copy goes when the initial execution
// request record is retired in favour of the Work's own facts.
func initialPreparation(request CreateWork, workID string) *PrepareWorkExecution {
	if request.InitialExecution == nil {
		return nil
	}
	retained := *request.InitialExecution
	retained.WorkID = workID
	return &retained
}

// A competing creation may establish this Subject's Work after the first
// lookup but before admission is refused. Reusing that Work needs no new
// capacity.
func (h *CreateWorkHandler) reuseAfterRejection(ctx context.Context, agent *domain.AgentDefinition, request CreateWork, fingerprint string, rejection error) (string, error) {
	if errors.Is(rejection, ErrDeskUnavailable) || errors.Is(rejection, ErrStartRequestConflict) {
		return "", rejection
	}
	work, err := agent.ReuseWork(ctx, request.Subject, h.lookup)
	if err != nil {
		return "", err
	}
	if work == nil {
		return "", rejection
	}
	return h.reused(ctx, request, fingerprint, work.ID)
}
