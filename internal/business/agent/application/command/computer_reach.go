package command

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ComputerWorks lists a Computer's Works in identity order after afterID.
type ComputerWorks interface {
	// ListRunningWorkIDs lists those whose run may still be in an execution
	// environment on it: Starting past its Desk, Working, Stopping, or
	// Closing before its compute release.
	ListRunningWorkIDs(ctx context.Context, computerID, afterID string, limit int) ([]string, error)
	// ListHeldWorkIDs lists those whose run's compute is not released, a
	// start waiting in its line and a Failed Work included.
	ListHeldWorkIDs(ctx context.Context, computerID, afterID string, limit int) ([]string, error)
}

// computerWorksPage bounds one page of a Computer's running Works.
const computerWorksPage = 100

// ComputerReachHandler records each Managed Work's Disconnection from its
// Computer's Presence and stops it beyond reach past the Disconnection
// timeout. It always applies the Presence Computer holds now, never the fact
// that woke it, so a repeated, lost or reordered Presence fact only delays
// the record; the Work's recovery applies it too. A Cloud Work's recovery
// re-reads Dispatcher's attachment to its sandbox instead
// (recoverAttachment).
type ComputerReachHandler struct {
	now       func() time.Time
	sessions  domain.AgentWorkRepository
	computers ComputerPort
	works     ComputerWorks
	execution domain.ExecutionPort
	processes ProcessObservationPort
	buffer    RunningHarnessPort
	places    presenceReads
	logger    *slog.Logger
}

func NewComputerReachHandler(now func() time.Time, sessions domain.AgentWorkRepository, computers ComputerPort, works ComputerWorks, execution domain.ExecutionPort, processes ProcessObservationPort, buffer RunningHarnessPort, logger *slog.Logger) *ComputerReachHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ComputerReachHandler{now: now, sessions: sessions, computers: computers, works: works, execution: execution, processes: processes, buffer: buffer, places: presenceReads{read: map[string]presenceRead{}}, logger: logger}
}

// presenceShared is how long one read of a Computer's Presence serves the
// recovery of every Work on it, so a sweep asks Computer once per Computer,
// not once per Work. A Presence fact is applied late by at most this much,
// far inside the margin by which a machine fences before the Disconnection
// timeout, so a stop beyond reach stays true.
const presenceShared = 10 * time.Second

// presenceReads keeps this replica's latest read of each Computer's Presence
// for presenceShared. Every replica reads for itself; nothing is shared
// between them.
type presenceReads struct {
	mu   sync.Mutex
	read map[string]presenceRead
}

type presenceRead struct {
	place ComputerDescription
	at    time.Time
}

func (r *presenceReads) get(computerID string, now time.Time) (ComputerDescription, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	read, ok := r.read[computerID]
	if !ok || now.Sub(read.at) >= presenceShared || now.Before(read.at) {
		return ComputerDescription{}, false
	}
	return read.place, true
}

// put keeps a fresh read and forgets the expired ones, so the reads kept
// stay bounded by the Computers recently asked about.
func (r *presenceReads) put(computerID string, place ComputerDescription, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, read := range r.read {
		if now.Sub(read.at) >= presenceShared {
			delete(r.read, id)
		}
	}
	r.read[computerID] = presenceRead{place: place, at: now}
}

// describe reads the Computer's Presence, sharing a recent read unless a
// fresh one is asked for.
func (h *ComputerReachHandler) describe(ctx context.Context, computerID string, fresh bool) (ComputerDescription, error) {
	now := h.now()
	if !fresh {
		if place, ok := h.places.get(computerID, now); ok {
			return place, nil
		}
	}
	place, err := h.computers.DescribeComputer(ctx, computerID)
	if err != nil {
		return ComputerDescription{}, err
	}
	h.places.put(computerID, place, now)
	return place, nil
}

// reachAttempts bounds retries of a Work changed concurrently.
const reachAttempts = 3

// PresenceChanged applies a Managed Computer's current Presence to every Work
// running on it. A Work that cannot be changed now does not hold back the
// others, nor the fact: its failure is logged and its recovery applies the
// Presence later. Only a Presence or listing that cannot be read is
// returned, for the fact's retry.
func (h *ComputerReachHandler) PresenceChanged(ctx context.Context, computerID string) error {
	// The change that woke it is read fresh, and the read then serves the
	// recoveries that follow.
	place, err := h.describe(ctx, computerID, true)
	if err != nil {
		return err
	}
	// A Cloud's Works disconnect alone, from Dispatcher's attachment, and a
	// removed Computer's environments are lost, not disconnected.
	if !place.Managed || place.Removed {
		return nil
	}
	after := ""
	for {
		ids, err := h.works.ListRunningWorkIDs(ctx, computerID, after, computerWorksPage)
		if err != nil || len(ids) == 0 {
			return err
		}
		for _, id := range ids {
			if err := h.applyTo(ctx, id, place); err != nil {
				h.logger.WarnContext(ctx, "agent: Presence not applied to a Work; its recovery applies it", "computer_id", computerID, "work_id", id, "error", err)
			}
		}
		after = ids[len(ids)-1]
	}
}

func (h *ComputerReachHandler) applyTo(ctx context.Context, workID string, place ComputerDescription) error {
	var err error
	for range reachAttempts {
		var work *domain.AgentWork
		work, err = h.sessions.Get(ctx, workID)
		if err != nil {
			return err
		}
		requests := deferExecution(h.execution)
		var changed bool
		if changed, err = h.apply(ctx, work, place, requests); err != nil || !changed {
			return err
		}
		if err = h.sessions.Save(ctx, work); !errors.Is(err, domain.ErrConcurrentModification) {
			if err != nil {
				return err
			}
			return requests.send(ctx)
		}
	}
	return err
}

// recover applies the current observation of the Work's execution
// environment during its recovery; it reports whether the Work changed. A
// Managed Work whose run may still be on its machine asks its Computer's
// Presence, sharing a recent read of its Computer with the other Works on
// it; a Cloud Work follows Dispatcher's attachment.
func (h *ComputerReachHandler) recover(ctx context.Context, work *domain.AgentWork, requests *deferredExecution) (bool, error) {
	if h == nil {
		return false, nil
	}
	if !work.OnManagedComputer() {
		return h.recoverAttachment(ctx, work, requests)
	}
	if !work.ObservesEnvironment() {
		return false, nil
	}
	place, err := h.describe(ctx, work.ComputerID, false)
	if err != nil {
		return false, err
	}
	if !place.Managed || place.Removed {
		return false, nil
	}
	return h.apply(ctx, work, place, requests)
}

// apply records or ends the Work's Disconnection from its Computer's
// Presence, then stops it beyond reach when its Disconnection has lasted the
// Disconnection timeout.
func (h *ComputerReachHandler) apply(ctx context.Context, work *domain.AgentWork, place ComputerDescription, requests *deferredExecution) (bool, error) {
	if !work.OnManagedComputer() {
		return false, nil
	}
	if place.Reachable {
		return work.EndDisconnection(), nil
	}
	since := place.UnreachableSince
	if since.IsZero() {
		// Computer always records since when; without it, a Disconnection
		// under way keeps its origin and a new one starts now.
		since = work.DisconnectedSince
		if since.IsZero() {
			since = h.now()
		}
	}
	recorded, err := work.RecordDisconnection(since)
	if err != nil {
		return false, err
	}
	pending, err := h.inputAwaits(ctx, work)
	if err != nil {
		return recorded, err
	}
	stopped, err := work.StopBeyondReach(ctx, h.now(), pending, requests)
	return recorded || stopped, err
}

// inputAwaits supplies the buffered-input fact only to a Work whose
// Disconnection is due, so applying a Presence reads the buffer rarely.
func (h *ComputerReachHandler) inputAwaits(ctx context.Context, work *domain.AgentWork) (bool, error) {
	if !work.DisconnectionDue(h.now()) {
		return false, nil
	}
	buffered, err := h.buffer.ReadBuffer(ctx, work.ID)
	if err != nil {
		return false, err
	}
	return buffered.Pending(), nil
}
