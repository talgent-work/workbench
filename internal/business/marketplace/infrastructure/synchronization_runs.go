package infrastructure

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"xorm.io/xorm"
)

// SynchronizationRuns stores only the current platform operation. Reservation
// serializes triggers across replicas; the existing content lock serializes publication.
type SynchronizationRuns struct{ engine *xorm.Engine }

func NewSynchronizationRuns(engine *xorm.Engine) *SynchronizationRuns {
	return &SynchronizationRuns{engine}
}

type synchronizationRunDO struct {
	ID          string `xorm:"id"`
	Status      uint8  `xorm:"status"`
	Force       bool   `xorm:"force_refresh"`
	RequestedAt int64  `xorm:"created_at"`
	Deadline    int64  `xorm:"deadline"`
	FinishedAt  int64  `xorm:"finished_at"`
	Failures    string `xorm:"failures"`
}

func (s *SynchronizationRuns) Reserve(ctx context.Context, candidate application.SynchronizationRun) (*application.SynchronizationRun, error) {
	session := s.engine.NewSession()
	defer session.Close()
	session = session.Context(ctx)
	if err := session.Begin(); err != nil {
		return nil, err
	}
	defer session.Rollback()
	_, err := session.Exec(`INSERT INTO marketplace_sync_run (id,slot,status,force_refresh,deadline,finished_at,failures,version,created_at,updated_at,deleted_at) VALUES (?,'catalog',1,?,?,0,'[]',1,?,?,0) ON DUPLICATE KEY UPDATE slot=slot`, candidate.ID, candidate.Force, candidate.Deadline, candidate.RequestedAt, candidate.RequestedAt)
	if err != nil {
		return nil, err
	}
	var row synchronizationRunDO
	if _, err := session.SQL(`SELECT * FROM marketplace_sync_run WHERE slot='catalog' AND deleted_at=0 FOR UPDATE`).Get(&row); err != nil {
		return nil, err
	}
	if row.ID != candidate.ID && (row.Status != 1 && row.Status != 2 || row.Deadline <= candidate.RequestedAt) {
		_, err = session.Exec(`UPDATE marketplace_sync_run SET id=?,status=1,force_refresh=?,deadline=?,finished_at=0,failures='[]',created_at=?,updated_at=?,version=version+1 WHERE slot='catalog' AND id=? AND deleted_at=0`, candidate.ID, candidate.Force, candidate.Deadline, candidate.RequestedAt, candidate.RequestedAt, row.ID)
		if err != nil {
			return nil, err
		}
		row = synchronizationRunDO{ID: candidate.ID, Status: 1, Force: candidate.Force, RequestedAt: candidate.RequestedAt, Deadline: candidate.Deadline, Failures: "[]"}
	}
	if err := session.Commit(); err != nil {
		return nil, err
	}
	return decodeSynchronizationRun(row)
}
func (s *SynchronizationRuns) Latest(ctx context.Context) (*application.SynchronizationRun, error) {
	var row synchronizationRunDO
	found, err := s.engine.Context(ctx).SQL(`SELECT * FROM marketplace_sync_run WHERE slot='catalog' AND deleted_at=0`).Get(&row)
	if err != nil || !found {
		return nil, err
	}
	return decodeSynchronizationRun(row)
}
func (s *SynchronizationRuns) Claim(ctx context.Context, id string, now int64) (*application.SynchronizationRun, error) {
	result, err := s.engine.Context(ctx).Exec(`UPDATE marketplace_sync_run SET status=2,updated_at=?,version=version+1 WHERE id=? AND status=1 AND deadline>? AND deleted_at=0`, now, id, now)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return nil, err
	}
	run, err := s.Latest(ctx)
	if run != nil && run.ID != id {
		return nil, nil
	}
	return run, err
}
func (s *SynchronizationRuns) Finish(ctx context.Context, id, status string, failures []application.SynchronizationFailure, now int64) error {
	if failures == nil {
		failures = []application.SynchronizationFailure{}
	}
	encoded, err := json.Marshal(failures)
	if err != nil {
		return err
	}
	result, err := s.engine.Context(ctx).Exec(`UPDATE marketplace_sync_run SET status=?,failures=?,finished_at=?,updated_at=?,version=version+1 WHERE id=? AND status=2 AND deleted_at=0`, synchronizationStatusCode(status), string(encoded), now, now, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("marketplace: synchronization reservation superseded")
	}
	return nil
}
func decodeSynchronizationRun(row synchronizationRunDO) (*application.SynchronizationRun, error) {
	run := &application.SynchronizationRun{ID: row.ID, Status: synchronizationStatuses[row.Status], Force: row.Force, RequestedAt: row.RequestedAt, Deadline: row.Deadline, FinishedAt: row.FinishedAt}
	if err := json.Unmarshal([]byte(row.Failures), &run.Failures); err != nil {
		return nil, err
	}
	return run, nil
}

var synchronizationStatuses = map[uint8]string{1: "queued", 2: "running", 3: "succeeded", 4: "partial_failure", 5: "failed", 6: "timed_out"}

func synchronizationStatusCode(status string) uint8 {
	for code, name := range synchronizationStatuses {
		if name == status {
			return code
		}
	}
	return 5
}
