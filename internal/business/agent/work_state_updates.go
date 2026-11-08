package agent

import (
	"context"
	"log/slog"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
	"xorm.io/xorm"
)

// committedWorks decorates persistence at composition, after the transaction.
// A live frame is best effort. Its failure cannot undo a committed Session; reads
// and reconnect continue using the same authoritative work projection. The
// Work's Domain Events are dispatched once its change is committed.
type committedWorks struct {
	*infrastructure.AgentWorkRepository
	*workStateUpdates
	events domainevent.Dispatcher
}

type workStateUpdates struct {
	reader query.WorkQueryRepository
	stream eventhandler.LiveStreamPort
	logger *slog.Logger
	// Recovery wakes are wired by RecoveryModule; without it a commit only
	// publishes its live frame.
	wakeWork func(context.Context, string)
}

func newWorkStateUpdates(reader query.WorkQueryRepository, stream eventhandler.LiveStreamPort, logger *slog.Logger) *workStateUpdates {
	return &workStateUpdates{reader: reader, stream: stream, logger: logger}
}

// newLiveStreamPublisher frames a Work's state as a reader's admission does.
func newLiveStreamPublisher(conn *pkgnats.LazyConn) (eventhandler.LiveStreamPort, error) {
	return infrastructure.NewLiveStreamPublisher(conn, connectrpc.WorkStateFrame)
}
func newCommittedWorks(db *xorm.Engine, now func() time.Time, updates *workStateUpdates, events domainevent.Dispatcher) *committedWorks {
	return &committedWorks{AgentWorkRepository: infrastructure.NewAgentWorkRepository(db, now), workStateUpdates: updates, events: events}
}

// committed follows a committed change of the Work.
func (r *committedWorks) committed(ctx context.Context, work *domain.AgentWork) {
	r.workCommitted(ctx, work.ID)
	if work.Events == nil || work.Events.Len() == 0 {
		return
	}
	// A lost event is made up by recovery, which reads the retained facts,
	// except WorkClosed: no fact retains its reclamation request, so a lost
	// one leaves the workspace in place, an accepted leak.
	if err := r.events.DispatchAll(ctx, work.Events.Drain()); err != nil {
		r.logger.WarnContext(ctx, "dispatch Work events failed", slog.String("agent_work_id", work.ID), slog.Any("error", err))
	}
}
func (r *committedWorks) Save(ctx context.Context, session *domain.AgentWork) error {
	if err := r.AgentWorkRepository.Save(ctx, session); err != nil {
		return err
	}
	r.committed(ctx, session)
	return nil
}

// workCommitted follows a committed Work change: its live frame, and the
// Work's recovery, whose obligations follow the Work.
func (r *workStateUpdates) workCommitted(ctx context.Context, id string) {
	r.publish(ctx, id)
	wake(ctx, r.wakeWork, id)
}

func wake(ctx context.Context, fn func(context.Context, string), id string) {
	if fn != nil {
		fn(ctx, id)
	}
}

func (r *workStateUpdates) publish(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	state, err := r.reader.GetWorkView(ctx, id)
	if err == nil {
		err = r.stream.PublishWorkState(ctx, state)
	}
	if err != nil {
		r.logger.WarnContext(ctx, "publish committed Session state failed", slog.String("agent_work_id", id), slog.Any("error", err))
	}
}
