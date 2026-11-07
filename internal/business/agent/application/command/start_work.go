package command

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var ErrStartRequestCanceled = errors.New("agent: start request canceled")

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
	sessions      AgentWorkRepository
	agents        domain.AgentDefinitionRepository
	authorization access.WorkAuthorizer
	computers     ComputerPort
	models        query.ModelConfigurations
	plugins       PluginUpgrades
}

type startBehavior func(*domain.AgentWork, domain.StartAdmission) error

// admissionAttempts bounds retries after the Work changed between reading it
// and committing this start.
const admissionAttempts = 3

// apply admits one start request. admitted is true once the start is durably
// admitted, even when the following delivery to execution failed; Agent's
// execution recovery owns that delivery obligation.
func (h workStartup) apply(ctx context.Context, request StartWork, begin startBehavior, kind func(*domain.AgentWork) query.StartKind, acceptedKinds ...query.StartKind) (bool, error) {
	if err := h.authorization.AuthorizeWork(ctx, request.SubjectID, request.WorkID); err != nil {
		return false, err
	}
	if uuid.Validate(request.RequestID) != nil {
		return false, query.ErrInvalidStartRequest
	}
	for attempt := 1; ; attempt++ {
		admitted, err := h.admit(ctx, request, begin, kind, acceptedKinds)
		if admitted || !errors.Is(err, domain.ErrConcurrentModification) || attempt == admissionAttempts {
			return admitted, err
		}
	}
}

func (h workStartup) admit(ctx context.Context, request StartWork, begin startBehavior, kind func(*domain.AgentWork) query.StartKind, acceptedKinds []query.StartKind) (bool, error) {
	if found, err := h.admitted(ctx, request, acceptedKinds); err != nil || found {
		return found, err
	}
	session, err := h.sessions.Get(ctx, request.WorkID)
	if err != nil {
		return false, err
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
	// Each start takes up the current releases of the selected Plugins and the
	// definition's current Soul (Agent model: Capability stability).
	current, err := h.plugins.CurrentReleases(ctx, request.SubjectID, session.CapabilitySnapshot.Plugins)
	if err != nil {
		return false, err
	}
	admission := domain.StartAdmission{AdmittedAt: h.now(), AgentID: agent.ID, ModelConfigurationProvided: provided, Soul: agent.Soul, CurrentPlugins: current}
	if session.WorkModel.Available(provided) {
		// Only a Work that may start now asks its Computer for room.
		if err := admitStartKind(session, kind(session)); err != nil {
			return false, err
		}
		// A sandboxed Work is admitted only where the sandbox is provided:
		// the platform never runs it without (Agent model: Managed sandbox).
		seat, err := admitRun(ctx, h.computers, RunAdmission{ComputerID: session.ComputerID, WorkID: session.ID, WorkOwnerID: agent.WorkPrincipal(), StarterID: request.SubjectID, Sandbox: session.CapabilitySnapshot.Sandbox})
		if err != nil {
			return false, h.refused(ctx, session, kind(session), err)
		}
		admission.DefinitionAdmitted, admission.Seat = true, seat
	}
	start := query.AdmittedStart{WorkID: session.ID, Kind: kind(session)}
	if err := begin(session, admission); err != nil {
		h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
		return false, err
	}
	// The start's execution begins only once the Work and its start request
	// commit.
	if err := h.sessions.CommitStart(ctx, session, start, request.RequestID); err != nil {
		h.computers.ReleaseUnusedDesk(ctx, admission.Seat.DeskID)
		if errors.Is(err, domain.ErrConcurrentModification) {
			// A concurrent retry of this request may have won.
			if found, lookupErr := h.admitted(ctx, request, acceptedKinds); lookupErr != nil || found {
				return found, lookupErr
			}
		}
		return false, err
	}
	return true, nil
}

// refused records on the Work what its Computer's refusal of a Resume leaves
// (Retry on reconnection) and answers the refusal; a failed record answers
// its own error, a concurrent change among them, so the admission is tried
// again. A failure to ask the Computer is no refusal and records nothing.
func (h workStartup) refused(ctx context.Context, session *domain.AgentWork, kind query.StartKind, refusal error) error {
	if kind != query.StartKindResume || continuationRejection(refusal) == "" || !session.RefuseResume(refusal) {
		return refusal
	}
	if err := h.sessions.Save(ctx, session); err != nil {
		return err
	}
	return refusal
}

// admitted reports whether this request was already admitted for the Work,
// making a repeated request idempotent.
func (h workStartup) admitted(ctx context.Context, request StartWork, acceptedKinds []query.StartKind) (bool, error) {
	accepted, found, err := h.sessions.FindAdmittedStart(ctx, request.RequestID)
	if err != nil || !found {
		return false, err
	}
	if accepted.WorkID != request.WorkID || !slices.Contains(acceptedKinds, accepted.Kind) {
		return false, ErrStartRequestConflict
	}
	return true, nil
}

var ErrStartRequestConflict = errors.New("agent: start request identity belongs to another operation")

// admitStartKind refuses a start the Work cannot begin now.
func admitStartKind(work *domain.AgentWork, kind query.StartKind) error {
	if kind == query.StartKindRestart {
		return work.AdmitRestart()
	}
	return work.AdmitResume()
}

func fixedKind(kind query.StartKind) func(*domain.AgentWork) query.StartKind {
	return func(*domain.AgentWork) query.StartKind { return kind }
}

type ResumeWorkHandler struct{ continuation workContinuation }
type RestartWorkHandler struct{ startup workStartup }

func NewResumeWorkHandler(now func() time.Time, sessions AgentWorkRepository, agents domain.AgentDefinitionRepository, authorization access.WorkAuthorizer, computers ComputerPort, requests ContinuationRequests, models query.ModelConfigurations, plugins PluginUpgrades) *ResumeWorkHandler {
	return &ResumeWorkHandler{continuation: workContinuation{startup: workStartup{now: now, sessions: sessions, agents: agents, authorization: authorization, computers: computers, models: models, plugins: plugins}, requests: requests}}
}
func NewRestartWorkHandler(now func() time.Time, sessions AgentWorkRepository, agents domain.AgentDefinitionRepository, authorization access.WorkAuthorizer, computers ComputerPort, models query.ModelConfigurations, plugins PluginUpgrades) *RestartWorkHandler {
	return &RestartWorkHandler{startup: workStartup{now: now, sessions: sessions, agents: agents, authorization: authorization, computers: computers, models: models, plugins: plugins}}
}
func (h *ResumeWorkHandler) Handle(ctx context.Context, request StartWork) error {
	return h.continuation.handle(ctx, request, "resume")
}
func (h *RestartWorkHandler) Handle(ctx context.Context, request StartWork) (bool, error) {
	return h.startup.apply(ctx, request, (*domain.AgentWork).Restart, fixedKind(query.StartKindRestart), query.StartKindRestart)
}

// Recover observes the retained caller outcome; it never submits execution.
func (h *ResumeWorkHandler) Recover(ctx context.Context, sessionID string) error {
	return h.continuation.recover(ctx, sessionID)
}
