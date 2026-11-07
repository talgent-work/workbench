package infrastructure

import (
	"context"
	"encoding/json"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

type WorkReportQueryRepository struct{ db *xorm.Engine }

func NewWorkReportQueryRepository(db *xorm.Engine) *WorkReportQueryRepository {
	return &WorkReportQueryRepository{db: db}
}

var _ query.WorkReportReader = (*WorkReportQueryRepository)(nil)

type workHistoryRow struct {
	SessionID                            string `xorm:"session_id"`
	AgentOwner                           string `xorm:"agent_owner"`
	SessionCreatedAt                     int64
	SourceVersion                        int
	State                                uint8
	Started, Failed, Waiting             bool
	ObservedAt                           int64
	CurrentState                         uint8
	CurrentRevision                      int
	CurrentProcess, CurrentFailureReason string
}

func (r *WorkReportQueryRepository) ReadWorkHistory(ctx context.Context, scope query.WorkReportScope, before time.Time) ([]query.WorkHistory, error) {
	// One statement observes a coherent population including permanently closed
	// Work. It never uses a caller's board, the personal Recent list, or their limits.
	// ponytail: memory grows with a project's state transitions; replace with a
	// streaming fold or period projection if large-project report latency grows.
	var rows []workHistoryRow
	q := r.db.Context(ctx).Table("agent_session").Alias("s")
	if scope.Purpose != "" {
		q = q.Join("INNER", []string{"agent_definition", "a"}, "a.id=s.agent_id AND a.purpose=? AND a.deleted_at=0", scope.Purpose)
	}
	q = q.Join("LEFT", []string{"agent_session_state_history", "h"}, "h.session_id=s.id AND h.deleted_at=0 AND h.observed_at < ?", before.UnixMilli()).
		Select("s.id AS session_id,"+agentOwnerColumn+" AS agent_owner,s.created_at AS session_created_at,COALESCE(h.source_version,0) AS source_version,COALESCE(h.state,0) AS state,COALESCE(h.started,0) AS started,COALESCE(h.failed,0) AS failed,COALESCE(h.waiting,0) AS waiting,COALESCE(h.observed_at,0) AS observed_at,s.state AS current_state,s.version AS current_revision,s.harness_process AS current_process,s.failure_reason AS current_failure_reason").
		Where("s.deleted_at=0 AND s.created_at<?", before.UnixMilli())
	err := q.And("s.project_ref=?", scope.ProjectID).Asc("s.id", "h.source_version").Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "agent.read_work_history").Wrap(err)
	}
	result := make([]query.WorkHistory, 0)
	for _, row := range rows {
		if len(result) == 0 || result[len(result)-1].WorkID != row.SessionID {
			if int(row.CurrentState) >= len(sessionStates) {
				return nil, oops.Errorf("agent: invalid current Session state %d", row.CurrentState)
			}
			current := &query.WorkStateFact{Revision: row.CurrentRevision, State: sessionStates[row.CurrentState], Failed: row.CurrentFailureReason != "" || sessionStates[row.CurrentState] == domain.WorkFailed}
			var process *harnessProcessRecord
			if row.CurrentProcess != "" {
				if err := json.Unmarshal([]byte(row.CurrentProcess), &process); err != nil {
					return nil, oops.Wrap(err)
				}
			}
			if process != nil {
				current.Started = process.Ready
				current.Waiting = process.waiting()
			}
			result = append(result, query.WorkHistory{WorkID: row.SessionID, AgentOwner: row.AgentOwner, CreatedAt: time.UnixMilli(row.SessionCreatedAt).UTC(), Current: current})
		}
		if row.SourceVersion == 0 {
			continue
		}
		if int(row.State) >= len(sessionStates) {
			return nil, oops.Errorf("agent: invalid historical Session state %d", row.State)
		}
		work := &result[len(result)-1]
		work.Facts = append(work.Facts, query.WorkStateFact{Revision: row.SourceVersion, At: time.UnixMilli(row.ObservedAt).UTC(), State: sessionStates[row.State], Started: row.Started, Failed: row.Failed, Waiting: row.Waiting})
	}
	return result, nil
}
