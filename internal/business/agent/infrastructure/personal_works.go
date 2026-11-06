package infrastructure

import (
	"cmp"
	"context"
	"slices"

	"github.com/samber/oops"
	"xorm.io/builder"

	"github.com/talgent-work/workbench/internal/business/agent/application/personalwork"
)

var _ personalwork.Works = (*WorkQueryRepository)(nil)

// personalWorkCondition selects a definition's Works without a Subject or a
// Project: its Personal Works.
func personalWorkCondition(agentID string) builder.Cond {
	return builder.Eq{"s.agent_id": agentID, "s.project_ref": ""}.And(builder.IsNull{"s.subject_kind"})
}

type personalWorkRow struct {
	ID             string `xorm:"id"`
	DisplayTitle   string `xorm:"display_title"`
	State          uint8  `xorm:"state"`
	HarnessProcess string `xorm:"harness_process"`
	StopReason     string `xorm:"stop_reason"`
	Disconnected   int64  `xorm:"disconnected_since"`
	Failure        string `xorm:"failure_reason"`
	CreatedAt      int64  `xorm:"created_at"`
	UpdatedAt      int64  `xorm:"updated_at"`
}

func (r *WorkQueryRepository) ListPersonalWorks(ctx context.Context, agentID string, limit int) ([]personalwork.PersonalWorkSummary, error) {
	var rows []personalWorkRow
	err := r.db.Context(ctx).Table("agent_session").Alias("s").
		Select("s.id,s.display_title,s.state,s.harness_process,s.stop_reason,s.disconnected_since,s.failure_reason,s.created_at,s.updated_at").
		Where("s.deleted_at=0").And(personalWorkCondition(agentID)).
		Desc("s.updated_at", "s.created_at", "s.id").Limit(limit).Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "agent.list_personal_works").Wrap(err)
	}
	works := make([]personalwork.PersonalWorkSummary, 0, len(rows))
	for _, row := range rows {
		// The list shows the Work State as a Work view does.
		view, err := workViewFromRow(workViewRow{ID: row.ID, State: row.State, HarnessProcess: row.HarnessProcess, StopReason: row.StopReason, DisconnectedSince: row.Disconnected, Failure: row.Failure, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
		if err != nil {
			return nil, err
		}
		works = append(works, personalwork.PersonalWorkSummary{WorkID: view.WorkID, Title: row.DisplayTitle, State: view.State, Waiting: view.Waiting, StateReason: view.StateReason, DisconnectedSince: view.DisconnectedSince, CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt})
	}
	return works, nil
}

type libraryWorkRow struct {
	ID           string  `xorm:"id"`
	AgentID      string  `xorm:"agent_id"`
	SubjectKind  *string `xorm:"subject_kind"`
	ProjectRef   string  `xorm:"project_ref"`
	DisplayTitle string  `xorm:"display_title"`
	InputSource  string  `xorm:"input_source"`
	UpdatedAt    int64   `xorm:"updated_at"`
	Owner        string  `xorm:"agent_owner"`
}

// ListLibraryWorks reads the person's Personal Works and the Works with an
// input source shared with them, each by its own index, most recently updated
// first.
func (r *WorkQueryRepository) ListLibraryWorks(ctx context.Context, person, agentID string, limit int) ([]personalwork.LibraryWork, error) {
	read := func(condition builder.Cond) ([]libraryWorkRow, error) {
		var rows []libraryWorkRow
		err := r.db.Context(ctx).Table("agent_session").Alias("s").
			Select("s.id,s.agent_id,s.subject_kind,s.project_ref,s.display_title,s.input_source,s.updated_at,"+agentOwnerColumn+" AS agent_owner").
			Where("s.deleted_at=0").And(condition).Desc("s.updated_at", "s.id").Limit(limit).Find(&rows)
		return rows, oops.With("operation", "agent.list_library_works").Wrap(err)
	}
	own, err := read(personalWorkCondition(agentID))
	if err != nil {
		return nil, err
	}
	shared, err := read(builder.Expr("s.id IN (SELECT w.work_id FROM agent_work_share w WHERE w.reader_id=?) AND s.input_source<>''", person))
	if err != nil {
		return nil, err
	}
	rows := append(own, shared...)
	slices.SortStableFunc(rows, func(a, b libraryWorkRow) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	works := make([]personalwork.LibraryWork, 0, min(len(rows), limit))
	for _, row := range rows[:min(len(rows), limit)] {
		work := personalwork.LibraryWork{WorkID: row.ID, Title: row.DisplayTitle, InputSource: row.InputSource, Personal: row.ProjectRef == "" && row.SubjectKind == nil}
		if row.AgentID != agentID {
			work.SharedBy = row.Owner
		}
		works = append(works, work)
	}
	return works, nil
}
