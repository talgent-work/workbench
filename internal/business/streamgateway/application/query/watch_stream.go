package query

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type WatchStreamRequest struct {
	IncludeUnattributedErrors bool
	ActorUserID               string
	Admission                 ReaderAdmission
	Agent                     *string
	Kinds                     []string
	TurnIDs                   []string
	AgentWorkID               string
	After                     time.Time
	AfterID                   string
}

var ErrLiveUnavailable = errors.New("live stream unavailable")

// ReaderReadmissionInterval bounds how long an open stream outlives access
// Agent no longer grants when the withdrawal event itself was lost: the
// stream re-admits its reader on this interval.
const ReaderReadmissionInterval = time.Minute

type WatchStream struct {
	access      StreamAccessPort
	reader      UnifiedMessageReader
	registry    *HubRegistry
	logger      *slog.Logger
	settleDelay time.Duration
	readmission time.Duration
}

func NewWatchStream(reader UnifiedMessageReader, stream PortalLiveStream, logger *slog.Logger, access StreamAccessPort) *WatchStream {
	if logger == nil {
		logger = slog.Default()
	}
	return &WatchStream{
		access:      access,
		reader:      reader,
		registry:    NewHubRegistry(stream, logger),
		logger:      logger.With("component", "streamgateway.watch_stream"),
		settleDelay: ReplaySettleDelay,
		readmission: ReaderReadmissionInterval,
	}
}

func (w *WatchStream) SetSettleDelay(d time.Duration) {
	w.settleDelay = d
}

func (w *WatchStream) SetReadmissionInterval(d time.Duration) {
	w.readmission = d
}

func (w *WatchStream) Run(
	ctx context.Context,
	req WatchStreamRequest,
	send func(PortalLivePayload) error,
) error {
	sessionID := req.AgentWorkID
	if sessionID == "" {
		return ErrSessionIDRequired
	}
	// Subscribe before admitting, so an access withdrawal committed after the
	// admission is always observed by this stream.
	hub, sub, err := w.registry.Acquire(ctx, sessionID)
	if err != nil {
		return errors.Join(ErrLiveUnavailable, err)
	}
	defer w.registry.Release(hub, sub)
	starting, err := w.access.AdmitStreamReader(ctx, req.ActorUserID, req.Admission, sessionID)
	if err != nil {
		return err
	}
	// A reader opening mid-run sees the Work as it stands, not only its next change.
	if starting != nil {
		if err := send(&WorkStatePayload{Frame: starting}); err != nil {
			return err
		}
	}
	reader := req.ActorUserID
	withdrawing := func(payload PortalLivePayload) error {
		access, ok := payload.(*WorkAccessPayload)
		if !ok {
			return send(payload)
		}
		if !access.Withdraws(reader) {
			return nil
		}
		if err := send(&AccessWithdrawnPayload{AgentWorkID: sessionID}); err != nil {
			return err
		}
		return ErrWorkAccessWithdrawn
	}

	subscriber := NewSubscriber(SubscriberDeps{
		IncludeUnattributedErrors: req.IncludeUnattributedErrors, Reader: w.reader, LiveCh: sub.Live(), SettleDelay: w.settleDelay, Agent: req.Agent, Kinds: req.Kinds, TurnIDs: req.TurnIDs,
	})
	watching, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	go w.readmit(watching, stop, req, sessionID)
	err = subscriber.Run(watching, sessionID, req.After, req.AfterID, withdrawing)
	if ctx.Err() == nil && errors.Is(context.Cause(watching), ErrWorkAccessWithdrawn) {
		// The subscriber has stopped, so this is the stream's only sender.
		if sendErr := send(&AccessWithdrawnPayload{AgentWorkID: sessionID}); sendErr != nil {
			return sendErr
		}
		err = ErrWorkAccessWithdrawn
	}
	// These terminal conditions complete the subscription without returning an RPC
	// error. Record the suppressed reason here; middleware owns request completion.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		w.logger.InfoContext(ctx, "watch stream stopped by caller", slog.String("agent_work_id", sessionID), slog.String("cause", "client_context"))
		return nil
	}
	if errors.Is(err, ErrWorkAccessWithdrawn) {
		w.logger.InfoContext(ctx, "watch stream ended by access withdrawal", slog.String("agent_work_id", sessionID), slog.String("cause", "access_withdrawn"))
		return nil
	}
	if errors.Is(err, ErrLiveSubscriptionClosed) {
		w.logger.WarnContext(ctx, "watch stream live subscription closed", slog.String("agent_work_id", sessionID), slog.String("cause", "live_subscription_closed"))
		return nil
	}
	return err
}

// readmit asks Agent again on an interval and stops the stream once the
// reader is no longer admitted; an unavailable Agent leaves the stream open.
func (w *WatchStream) readmit(ctx context.Context, stop context.CancelCauseFunc, req WatchStreamRequest, workID string) {
	ticker := time.NewTicker(w.readmission)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		err := w.access.AuthorizeRead(ctx, req.ActorUserID, req.Admission, workID)
		if errors.Is(err, ErrWorkAccessDenied) {
			stop(ErrWorkAccessWithdrawn)
			return
		}
		if err != nil && ctx.Err() == nil {
			w.logger.WarnContext(ctx, "watch stream readmission unavailable", slog.String("agent_work_id", workID), slog.Any("error", err))
		}
	}
}
