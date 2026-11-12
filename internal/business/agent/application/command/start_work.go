package command

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type StartWork struct {
	ThinkingLevel string
	RequestID     string
	SubjectID     string
	WorkID        string
	// ExpectedVersion admits the start only for the Work version the caller observed.
	ExpectedVersion *int
}

// admitObservedVersion rejects a start decided on a Work version that has
// since changed; the caller must observe the current Work and decide again.
func (request StartWork) admitObservedVersion(work *domain.AgentWork) error {
	if request.ExpectedVersion != nil && *request.ExpectedVersion != work.Version {
		return domain.ErrConcurrentModification
	}
	return nil
}

type workStartup struct {
	now           func() time.Time
	sessions      domain.AgentWorkRepository
	agents        domain.AgentDefinitionRepository
	authorization access.WorkAuthorizer
	computers     ComputerPort
	models        query.ModelConfigurations
	plugins       PluginUpgrades
}

// startOperation is a Resume or a Restart: whether the Work may begin it now,
// and the AgentWork behavior that admits it.
type startOperation struct {
	resume bool
	admit  func(*domain.AgentWork) error
	begin  func(*domain.AgentWork, domain.StartAdmission) error
}

var (
	resumeOperation  = startOperation{resume: true, admit: (*domain.AgentWork).AdmitResume, begin: (*domain.AgentWork).Resume}
	restartOperation = startOperation{admit: (*domain.AgentWork).AdmitRestart, begin: (*domain.AgentWork).Restart}
)

// admissionAttempts bounds retries after the Work changed between reading it
// and committing this start.
const admissionAttempts = 3

// apply admits one start request. admitted is true once the start is durably
// admitted, even when the following delivery to execution failed; Agent's
// execution recovery owns that delivery obligation.
func (h workStartup) apply(ctx context.Context, request StartWork, operation startOperation) (bool, error) {
	if err := h.authorization.AuthorizeWork(ctx, request.SubjectID, request.WorkID); err != nil {
		return false, err
	}
	if uuid.Validate(request.RequestID) != nil {
		return false, ErrInvalidStartRequest
	}
	for attempt := 1; ; attempt++ {
		admitted, err := h.admit(ctx, request, operation)
		if admitted || !errors.Is(err, domain.ErrConcurrentModification) || attempt == admissionAttempts {
			return admitted, err
		}
	}
}

// admit makes one attempt. The request names its Desk, so a repeat asks for
// the same one: AgentWork recognizes a repeat whose Desk its current start
// holds and answers that admission, and Computer refuses a Desk that left, so
// a late repeat starts nothing (Unique start request).
func (h workStartup) admit(ctx context.Context, request StartWork, operation startOperation) (bool, error) {
	session, err := h.sessions.Get(ctx, request.WorkID)
	if err != nil {
		return false, err
	}
	deskID := requestedDeskID(request.WorkID, request.RequestID)
	if session.StartHoldsDesk(deskID) {
		return true, nil
	}
	agent, err := h.agents.Get(ctx, session.AgentID)
	if err != nil {
		return false, err
	}
	if err := request.admitObservedVersion(session); err != nil {
		return false, err
	}
	// Definition and capacity admission are consulted only for an available
	// model; admission refuses a withdrawn Model Configuration first.
	_, provided := h.models.Option(session.WorkModel)
	// Each start takes the current release declarations of the selected
	// Plugins and the definition's current Soul (Agent model: Start upgrade).
	current, err := h.plugins.CurrentReleases(ctx, request.SubjectID, session.CapabilitySnapshot)
	if err != nil {
		return false, err
	}
	admission := domain.StartAdmission{AdmittedAt: h.now(), AgentID: agent.ID, ModelConfigurationProvided: provided, Soul: agent.Soul, CurrentPlugins: current}
	if session.WorkModel.Available(provided) {
		// Only a Work that may start now asks its Computer for room.
		if err := operation.admit(session); err != nil {
			return false, err
		}
		// A sandboxed Work is admitted only where the sandbox is provided:
		// the platform never runs it without (Agent model: Managed sandbox).
		seat, err := admitRun(ctx, h.computers, RunAdmission{ComputerID: session.ComputerID, DeskID: deskID, WorkID: session.ID, WorkOwnerID: agent.WorkPrincipal(), StarterID: request.SubjectID, Sandbox: session.CapabilitySnapshot.Sandbox})
		if errors.Is(err, ErrDeskUnavailable) || errors.Is(err, ErrStartRequestConflict) {
			// Its Desk left without a committed start, or belongs to another
			// run: this request identity can no longer start.
			return false, err
		}
		if err != nil {
			return false, h.refused(ctx, session, operation, err)
		}
		admission.DefinitionAdmitted, admission.Seat = true, seat
	}
	if err := operation.begin(session, admission); err != nil {
		h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
		return false, err
	}
	// The start's execution begins only once the Work commits. A commit that
	// did not land leaves its Desk for the repeat, which asks for the same
	// one; the open-Desk reconciliation vacates it if none follows. After a
	// concurrent change the next attempt recognizes a repeat that won.
	if err := h.sessions.Save(ctx, session); err != nil {
		return false, err
	}
	if session.CapabilitySnapshot.StartupAgentDropped() {
		h.plugins.StartedWithoutStartupAgent(ctx, session)
	}
	return true, nil
}

// refused records on the Work what its Computer's refusal of a Resume leaves
// (Retry on reconnection) and answers the refusal; a failed record answers
// its own error, a concurrent change among them, so the admission is tried
// again. A failure to ask the Computer is no refusal and records nothing.
func (h workStartup) refused(ctx context.Context, session *domain.AgentWork, operation startOperation, refusal error) error {
	if !operation.resume || !refusedStart(refusal) || !session.RefuseResume(refusal) {
		return refusal
	}
	if err := h.sessions.Save(ctx, session); err != nil {
		return err
	}
	return refusal
}

// refusedStart reports a definitive refusal of a start: admission answered
// the request, which retains nothing beyond Reach Resume Due.
func refusedStart(err error) bool {
	for _, refusal := range startRefusals {
		if errors.Is(err, refusal) {
			return true
		}
	}
	return false
}

var startRefusals = []error{
	domain.ErrComputeReclamationPending, domain.ErrCapacityUnavailable, domain.ErrComputerNotUsable,
	domain.ErrComputerUnreachable, domain.ErrComputerRemoved, domain.ErrComputerOutdated, domain.ErrComputerNoSandbox,
	domain.ErrModelConfigurationWithdrawn, domain.ErrWorkTransition, domain.ErrExecutionNotPermitted,
	domain.ErrStartRequestSpent, ErrDeskUnavailable, ErrStartRequestConflict,
}

// ResumeWorkHandler admits a Resume and commits its start at once; a refusal answers the caller and retains nothing beyond
// Reach Resume Due (Capacity rejection state, Retry on reconnection).
type ResumeWorkHandler struct{ startup workStartup }
type RestartWorkHandler struct{ startup workStartup }

func NewResumeWorkHandler(now func() time.Time, sessions domain.AgentWorkRepository, agents domain.AgentDefinitionRepository, authorization access.WorkAuthorizer, computers ComputerPort, models query.ModelConfigurations, plugins PluginUpgrades) *ResumeWorkHandler {
	return &ResumeWorkHandler{startup: workStartup{now: now, sessions: sessions, agents: agents, authorization: authorization, computers: computers, models: models, plugins: plugins}}
}
func NewRestartWorkHandler(now func() time.Time, sessions domain.AgentWorkRepository, agents domain.AgentDefinitionRepository, authorization access.WorkAuthorizer, computers ComputerPort, models query.ModelConfigurations, plugins PluginUpgrades) *RestartWorkHandler {
	return &RestartWorkHandler{startup: workStartup{now: now, sessions: sessions, agents: agents, authorization: authorization, computers: computers, models: models, plugins: plugins}}
}
func (h *ResumeWorkHandler) Handle(ctx context.Context, request StartWork) error {
	_, err := h.startup.apply(ctx, request, resumeOperation)
	return err
}
func (h *RestartWorkHandler) Handle(ctx context.Context, request StartWork) (bool, error) {
	return h.startup.apply(ctx, request, restartOperation)
}
