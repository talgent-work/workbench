package command

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// create makes one attempt to establish the request's Work: it reuses the
// definition's Work for the request's Work Key, or derives a new Work and its
// Desk from the request identity, asks the Computer for admission and
// commits the Work in Starting. A failed commit leaves the Desk to a repeat,
// which asks for the same Desk, and otherwise to the open-Desk
// reconciliation; a known refusal releases it at once.
func (h *CreateWorkHandler) create(ctx context.Context, request CreateWork) (string, error) {
	agent, err := h.agents.Get(ctx, request.AgentID)
	if err != nil {
		return "", err
	}
	if owner := agent.RepresentedPerson(); request.StartingUserID != "" && owner != request.StartingUserID {
		return "", access.ErrAccessDenied
	}
	// Reuse precedes all personal and catalog reads: reusing an existing Work
	// cannot absorb new defaults, releases or capability choices.
	if existing, err := h.keyWork(ctx, agent, request); err != nil || existing != nil {
		if err != nil {
			return "", err
		}
		return h.created(ctx, request, existing.ID)
	}
	// Installed Plugins and Connectors belong to the represented human, or to
	// the owning Application of an Application's definition (Application as
	// owner); an automated start has no owner's defaults.
	catalogUser := request.StartingUserID
	if catalogUser == "" {
		catalogUser = request.SubjectID
	}
	defaultsOwner := request.StartingUserID
	if application := agent.OwningApplication(); application != "" {
		defaultsOwner = application
	}
	capabilities, err := resolveStartupCapabilities(ctx, h.catalog, catalogUser, defaultsOwner, domain.StartupCapabilityInput{
		BasePrompt: agent.Capabilities.Prompt, RequiredPlugins: h.builtIns.ComposeRequiredPlugins(request.WorkCaller, agent.Capabilities.RequiredPlugins, request.CapabilityDifferences.RequiredPlugins), DefaultStartupAgent: agent.WorkDefaults.StartupAgent,
		Prompt: request.CapabilityDifferences.Prompt, StartupAgent: request.CapabilityDifferences.StartupAgent, Plugins: request.CapabilityDifferences.Plugins,
		ConnectorChoices: request.CapabilityDifferences.Connectors, ApplicationEndpoints: request.CapabilityDifferences.ApplicationEndpoints,
	})
	if err != nil {
		return "", err
	}
	// Every authorization usable at creation is selected, independently of
	// Plugin and Connector choices: the represented person's, or the owning
	// Application's.
	authorizationInput := domain.WorkAuthorizationInput{OwnerID: agent.AuthorizationOwner()}
	if authorizationInput.OwnerID != "" {
		authorizationInput.Available, err = h.catalog.ListPersonalAuthorizations(ctx, authorizationInput.OwnerID)
		if err != nil {
			return "", err
		}
	}
	repositories, err := h.repositoryInput(ctx, agent.AuthorizationOwner(), request.InitialExecution)
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
	// A Computer the definition's Works may not run on is refused before a
	// run is seated there.
	if err := agent.AdmitComputer(managed); err != nil {
		return h.reuseAfterRejection(ctx, agent, request, err)
	}
	model := h.workModel(request.WorkModel, agent.WorkDefaults, managed)
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
	thinking := h.defaultThinkingLevel(model, agent.WorkDefaults)
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
	workID := request.workID()
	if available {
		seat, err := admitRun(ctx, h.computers, RunAdmission{ComputerID: computer.ID, DeskID: requestedDeskID(agent.WorkPrincipal(), request.RequestID), WorkID: workID, WorkOwnerID: agent.WorkPrincipal(), StarterID: request.SubjectID, Sandbox: harness.Sandbox})
		if err != nil {
			return h.reuseAfterRejection(ctx, agent, request, err)
		}
		admission.DefinitionAdmitted, admission.Seat = true, seat
	}
	facts, err := h.creationFacts(ctx, agent, request)
	if err != nil {
		h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
		return "", err
	}
	work, established, err := agent.CreateWork(domain.WorkCreationRequest{StartedBy: request.SubjectID, RepositoryInput: repositories, AuthorizationInput: authorizationInput, WorkModel: model, ComputerManaged: managed, HarnessChoice: harness, WorkCaller: request.WorkCaller, WorkKey: request.WorkKey, FileHome: request.FileHome, StartingUser: request.StartingUserID, AgentHarnessID: request.AgentHarness, Capabilities: capabilities, TraceAttributes: agent.WorkTraceAttributes(request.TraceAttributes), ThinkingLevel: thinking, Admission: admission, StartingContext: request.StartingContext, WorkID: workID, DisplayTitle: request.DisplayTitle, WorkLabels: request.WorkLabels, InitialTaskInput: initialInput}, facts)
	if err != nil {
		h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
		return "", err
	}
	if !established {
		// Another creation established the Work for this Work Key; its run
		// holds its own Desk, unless a repeat of this very request did.
		if work.ID != workID {
			h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
		}
		return h.created(ctx, request, work.ID)
	}
	// The save registers the Work against the definition revision it was
	// created under; a competing registration of the same identity or a
	// changed definition is a concurrent modification, after which the
	// next attempt finds the Work or creates it again.
	if err := h.sessions.Save(ctx, work); err != nil {
		return "", err
	}
	// The start's execution begins once the Work commits; everything after
	// follows from the Work's facts.
	return work.ID, nil
}

// A competing creation may establish this Work Key's Work after the first
// lookup but before admission is refused. Reusing that Work needs no new
// capacity.
func (h *CreateWorkHandler) reuseAfterRejection(ctx context.Context, agent *domain.AgentDefinition, request CreateWork, rejection error) (string, error) {
	if errors.Is(rejection, ErrDeskUnavailable) || errors.Is(rejection, ErrStartRequestConflict) {
		return "", rejection
	}
	work, err := h.keyWork(ctx, agent, request)
	if err != nil {
		return "", err
	}
	if work == nil {
		return "", rejection
	}
	return h.created(ctx, request, work.ID)
}

// keyWork is the definition's Work of the request's Work Key, nil when it has
// none or the request names no Work Key; a Work of another Work Caller or
// File Home is refused (Mismatched reuse).
func (h *CreateWorkHandler) keyWork(ctx context.Context, agent *domain.AgentDefinition, request CreateWork) (*domain.AgentWork, error) {
	var found *domain.AgentWork
	if request.WorkKey.Present() {
		var err error
		if found, err = h.sessions.FindByWorkKey(ctx, agent.ID, request.WorkKey); err != nil {
			return nil, err
		}
	}
	return agent.ReuseWork(request.WorkCaller, request.WorkKey, request.FileHome, found)
}

// creationFacts loads the Works a creation supplies to its definition: the
// Work Key's Work and the source Work of a requested starting context.
func (h *CreateWorkHandler) creationFacts(ctx context.Context, agent *domain.AgentDefinition, request CreateWork) (domain.WorkCreationFacts, error) {
	var facts domain.WorkCreationFacts
	if request.WorkKey.Present() {
		found, err := h.sessions.FindByWorkKey(ctx, agent.ID, request.WorkKey)
		if err != nil {
			return facts, err
		}
		facts.KeyWork = found
	}
	if source := request.StartingContext; source != nil && strings.TrimSpace(source.WorkID) != "" {
		found, err := h.sessions.Get(ctx, strings.TrimSpace(source.WorkID))
		if err != nil && !errors.Is(err, domain.ErrWorkNotFound) {
			return facts, err
		}
		facts.SourceWork = found
	}
	return facts, nil
}
