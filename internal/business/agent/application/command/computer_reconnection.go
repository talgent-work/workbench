package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ComputerReconnectionHandler applies a Managed Computer's return of reach:
// it reconciles the machine and requests once more the Resume each marked
// Work is owed (Retry on reconnection). It applies what Computer says now,
// never the fact that woke it, and each Work's retry is idempotent across
// replicas: the request identity follows the Work version that recorded the
// mark. AgentWork decides what the mark becomes; this handler only supplies
// the facts: the Work's buffered input and its Computer's answer.
type ComputerReconnectionHandler struct {
	computers    ComputerPort
	sessions     domain.AgentWorkRepository
	agents       domain.AgentDefinitionRepository
	works        query.ExecutionQueryRepository
	buffer       DispatcherPort
	resume       *ResumeWorkHandler
	environments ExecutionEnvironmentPort
	logger       *slog.Logger
}

func NewComputerReconnectionHandler(computers ComputerPort, sessions domain.AgentWorkRepository, agents domain.AgentDefinitionRepository, works query.ExecutionQueryRepository, buffer DispatcherPort, resume *ResumeWorkHandler, environments ExecutionEnvironmentPort, logger *slog.Logger) (*ComputerReconnectionHandler, error) {
	if computers == nil || sessions == nil || agents == nil || works == nil || buffer == nil || resume == nil || environments == nil {
		return nil, errors.New("agent: Computer, Works, definitions, input buffer, Resume and execution environments are required to apply a reconnection")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ComputerReconnectionHandler{computers: computers, sessions: sessions, agents: agents, works: works, buffer: buffer, resume: resume, environments: environments, logger: logger}, nil
}

// Reconnected reconciles a Managed Computer that is reachable now and retries
// the Resumes its marked Works are owed. A Work that cannot be retried now
// holds back neither the others nor the fact: its failure is logged, and its
// recovery retries it while the mark stays, at most
// domain.ReachResumeAttempts times. A failed reconciliation or
// listing is returned for the fact's retry.
func (h *ComputerReconnectionHandler) Reconnected(ctx context.Context, computerID string) error {
	reachable, err := h.reachable(ctx, computerID)
	if err != nil || !reachable {
		return err
	}
	var failures []error
	// A Resume retried below starts only after the reconciliation, which its
	// start performs itself when this one failed.
	if err := h.environments.ReconcileComputer(ctx, computerID); err != nil {
		failures = append(failures, fmt.Errorf("reconcile computer %s: %w", computerID, err))
	}
	after := ""
	for {
		ids, err := h.works.ListReachResumeWorkIDs(ctx, computerID, after, computerWorksPage)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		if len(ids) == 0 {
			return errors.Join(failures...)
		}
		for _, id := range ids {
			if err := h.retryResume(ctx, id); err != nil {
				h.logger.WarnContext(ctx, "agent: owed Resume not retried on reconnection; the Work's recovery retries it", "computer_id", computerID, "work_id", id, "error", err)
			}
		}
		after = ids[len(ids)-1]
	}
}

// RetryOwedResume retries the Resume a marked Work is owed when its Managed
// Computer is reachable now. The Work's recovery calls it, so a lost
// Computer Became Reachable, or a retry that failed, still reaches the Work.
func (h *ComputerReconnectionHandler) RetryOwedResume(ctx context.Context, workID string) error {
	work, err := h.sessions.Get(ctx, workID)
	if err != nil || !work.ReachResumeDue {
		return err
	}
	reachable, err := h.reachable(ctx, work.ComputerID)
	if err != nil || !reachable {
		return err
	}
	return h.retryResume(ctx, workID)
}

// reachable reports whether the Computer is a Managed one the platform
// reaches now.
func (h *ComputerReconnectionHandler) reachable(ctx context.Context, computerID string) (bool, error) {
	place, err := h.computers.DescribeComputer(ctx, computerID)
	if err != nil {
		return false, err
	}
	return place.Managed && !place.Removed && place.Reachable, nil
}

// retryResume retries the owed Resume once and, when the retry failed
// without an answer, counts the failure on the Work. The failure reaching
// domain.ReachResumeAttempts spends the mark, so nothing retries it again,
// and is logged as an error naming the Work and its Computer; the input
// stays buffered for the Work's next Resume.
func (h *ComputerReconnectionHandler) retryResume(ctx context.Context, workID string) error {
	err := h.requestResume(ctx, workID)
	if err == nil || ctx.Err() != nil {
		return err
	}
	var work *domain.AgentWork
	spent := false
	recorded := h.applyToWork(ctx, workID, func(current *domain.AgentWork) bool {
		work, spent = current, false
		if !current.ReachResumeDue {
			return false
		}
		spent = current.RecordResumeRetryFailure()
		return true
	})
	if recorded != nil || !spent {
		return errors.Join(err, recorded)
	}
	h.logger.ErrorContext(ctx, "owed resume abandoned", slog.String("work_id", workID), slog.String("computer_id", work.ComputerID),
		slog.Int("attempts", domain.ReachResumeAttempts), slog.Any("error", err))
	return nil
}

// requestResume supplies the Work's buffered input to its mark and, when the
// Work wants it, requests the Resume once more and supplies the Computer's
// refusal. An admitted start clears the mark itself.
func (h *ComputerReconnectionHandler) requestResume(ctx context.Context, workID string) error {
	work, err := h.sessions.Get(ctx, workID)
	if err != nil || !work.ReachResumeDue {
		return err
	}
	buffered, err := h.buffer.ReadBuffer(ctx, work.ID)
	if err != nil {
		return err
	}
	resume, changed := work.ResumeOnReconnection(buffered.Pending())
	if changed {
		return h.sessions.Save(ctx, work)
	}
	if !resume {
		return nil
	}
	agent, err := h.agents.Get(ctx, work.AgentID)
	if err != nil {
		return err
	}
	// A Managed Computer runs only its owner's own Agent's Works, started by
	// that owner, who therefore asks for the retry.
	request := StartWork{SubjectID: agent.WorkPrincipal(), WorkID: work.ID, RequestID: reachResumeRequestID(work)}
	err = h.resume.Handle(ctx, request)
	if err == nil || !refusedStart(err) {
		return err
	}
	return h.refused(ctx, workID, err)
}

// refused supplies the retried Resume's refusal to the Work. The admission
// may have applied it already, which then changes nothing.
func (h *ComputerReconnectionHandler) refused(ctx context.Context, workID string, refusal error) error {
	return h.applyToWork(ctx, workID, func(work *domain.AgentWork) bool { return work.RefuseResume(refusal) })
}

// applyToWork applies a change to the Work as it is now and saves it when it
// changed, retried on a concurrent change.
func (h *ComputerReconnectionHandler) applyToWork(ctx context.Context, workID string, apply func(*domain.AgentWork) bool) error {
	var err error
	for range reachAttempts {
		var work *domain.AgentWork
		if work, err = h.sessions.Get(ctx, workID); err != nil {
			return err
		}
		if !apply(work) {
			return nil
		}
		if err = h.sessions.Save(ctx, work); !errors.Is(err, domain.ErrConcurrentModification) {
			return err
		}
	}
	return err
}

// reachResumeRequestID identifies the retry of the Resume owed at the Work's
// current version, so replicas retrying the same mark ask once.
func reachResumeRequestID(work *domain.AgentWork) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "reach-resume:%s@%d", work.ID, work.Version)).String()
}
