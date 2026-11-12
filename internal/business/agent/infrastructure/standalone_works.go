package infrastructure

import (
	"cmp"
	"context"
	"slices"

	"github.com/samber/oops"
	"xorm.io/builder"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// standaloneWorkCondition selects a definition's Works without a Subject or a
// Project: its Standalone Works.
func standaloneWorkCondition(agentID string) builder.Cond {
	return builder.Eq{"s.agent_id": agentID, "s.project_ref": ""}.And(builder.IsNull{"s.subject_kind"})
}

type standaloneWorkRow struct {
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

func (r *WorkQueryRepository) ListStandaloneWorks(ctx context.Context, filter query.StandaloneWorksFilter) ([]query.StandaloneWorkSummary, error) {
	condition := standaloneWorkCondition(filter.AgentID).And(carryingWorkLabels(filter.Labels))
	if len(filter.States) > 0 {
		codes, err := stateCodes(filter.States)
		if err != nil {
			return nil, err
		}
		condition = condition.And(builder.In("s.state", codes))
	}
	var rows []standaloneWorkRow
	err := r.db.Context(ctx).Table("agent_session").Alias("s").
		Select("s.id,s.display_title,s.state,s.harness_process,s.stop_reason,s.disconnected_since,s.failure_reason,s.created_at,s.updated_at").
		Where("s.deleted_at=0").And(condition).
		Desc("s.updated_at", "s.created_at", "s.id").Limit(filter.Limit).Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "agent.list_standalone_works").Wrap(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	labels, err := listedWorkLabels(r.db.Context(ctx), ids)
	if err != nil {
		return nil, err
	}
	works := make([]query.StandaloneWorkSummary, 0, len(rows))
	for _, row := range rows {
		// The list shows the Work State as a Work view does.
		view, err := workViewFromRow(workViewRow{ID: row.ID, State: row.State, HarnessProcess: row.HarnessProcess, StopReason: row.StopReason, DisconnectedSince: row.Disconnected, Failure: row.Failure, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
		if err != nil {
			return nil, err
		}
		works = append(works, query.StandaloneWorkSummary{WorkID: view.WorkID, Title: row.DisplayTitle, Labels: labels[row.ID], State: view.State, Waiting: view.Waiting, StateReason: view.StateReason, DisconnectedSince: view.DisconnectedSince, CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt})
	}
	return works, nil
}

// stateCodes are the stored codes of the Work States.
func stateCodes(states []domain.WorkState) ([]uint8, error) {
	codes := make([]uint8, 0, len(states))
	for _, state := range states {
		code := slices.Index(sessionStates[:], state)
		if code <= 0 {
			return nil, oops.With("operation", "agent.list_standalone_works").Errorf("unknown work state %q", state)
		}
		codes = append(codes, uint8(code))
	}
	return codes, nil
}

type libraryWorkRow struct {
	ID           string  `xorm:"id"`
	AgentID      string  `xorm:"agent_id"`
	SubjectKind  *string `xorm:"subject_kind"`
	ProjectRef   string  `xorm:"project_ref"`
	DisplayTitle string  `xorm:"display_title"`
	Layout       string  `xorm:"located_layout"`
	UpdatedAt    int64   `xorm:"updated_at"`
	Owner        string  `xorm:"agent_owner"`
}

// ListLibraryWorks reads the person's Standalone Works and the Works shared
// with them, each by its own index, most recently updated first, each with
// the publication target its located layout fixed.
func (r *WorkQueryRepository) ListLibraryWorks(ctx context.Context, person, agentID string, limit int) ([]query.LibraryWork, error) {
	read := func(condition builder.Cond) ([]libraryWorkRow, error) {
		var rows []libraryWorkRow
		err := r.db.Context(ctx).Table("agent_session").Alias("s").
			Select("s.id,s.agent_id,s.subject_kind,s.project_ref,s.display_title,s.located_layout,s.updated_at,"+agentOwnerColumn+" AS agent_owner").
			Where("s.deleted_at=0").And(condition).Desc("s.updated_at", "s.id").Limit(limit).Find(&rows)
		return rows, oops.With("operation", "agent.list_library_works").Wrap(err)
	}
	own, err := read(standaloneWorkCondition(agentID))
	if err != nil {
		return nil, err
	}
	shared, err := read(builder.Expr("s.id IN (SELECT w.work_id FROM agent_work_share w WHERE w.reader_id=?)", person))
	if err != nil {
		return nil, err
	}
	rows := append(own, shared...)
	slices.SortStableFunc(rows, func(a, b libraryWorkRow) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	works := make([]query.LibraryWork, 0, min(len(rows), limit))
	for _, row := range rows[:min(len(rows), limit)] {
		layout, err := decodeLocatedLayout(row.Layout)
		if err != nil {
			return nil, err
		}
		work := query.LibraryWork{WorkID: row.ID, Title: row.DisplayTitle, PublicationTarget: layout.PublicationTarget.Directory, Standalone: row.ProjectRef == "" && row.SubjectKind == nil}
		if row.AgentID != agentID {
			work.SharedBy = row.Owner
		}
		works = append(works, work)
	}
	return works, nil
}
