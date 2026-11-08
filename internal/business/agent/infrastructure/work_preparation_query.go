package infrastructure

import (
	"context"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

var _ query.WorkPreparationReader = (*WorkQueryRepository)(nil)

type workPreparationRow struct {
	ID                  string `xorm:"id"`
	StartedBy           string `xorm:"started_by"`
	ThinkingLevel       string `xorm:"thinking_level"`
	InitialTaskInput    string `xorm:"initial_task_input"`
	RepositorySelection string `xorm:"repository_selection"`
	RepositoryCheckouts string `xorm:"repository_checkouts"`
}

// GetWorkPreparation derives the first start of a Work, which its creation
// request admitted with it, from its facts.
func (r *WorkQueryRepository) GetWorkPreparation(ctx context.Context, workID string) (query.WorkPreparation, bool, error) {
	preparations, err := r.preparations(ctx, "s.id = ?", workID)
	if err != nil || len(preparations) == 0 {
		return query.WorkPreparation{}, false, err
	}
	return preparations[0], true, nil
}

// ListSubmittedPreparations pages, by Work identity, through the first starts
// of Works: every such start was admitted with its Work.
func (r *WorkQueryRepository) ListSubmittedPreparations(ctx context.Context, after string, limit int) ([]query.WorkPreparation, error) {
	return r.preparations(ctx, "s.id > ? ORDER BY s.id LIMIT ?", after, limit)
}

func (r *WorkQueryRepository) preparations(ctx context.Context, condition string, args ...any) ([]query.WorkPreparation, error) {
	var rows []workPreparationRow
	err := r.db.Context(ctx).SQL(`SELECT s.id, s.started_by, s.thinking_level, s.initial_task_input, s.repository_selection, s.repository_checkouts
 FROM agent_session s WHERE s.deleted_at = 0
 AND `+condition, args...).Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "agent.work_preparations").Wrap(err)
	}
	result := make([]query.WorkPreparation, 0, len(rows))
	for _, row := range rows {
		preparation, err := workPreparationFromRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, preparation)
	}
	return result, nil
}

// workPreparationFromRow maps a Work's facts to its first start: submitted with
// the Work, its workspace prepared once every selected
// repository has a checkout outcome, and its initial task input buffered once
// the Work no longer holds it.
func workPreparationFromRow(row workPreparationRow) (query.WorkPreparation, error) {
	initial, err := decodeInitialTaskInput(row.InitialTaskInput)
	if err != nil {
		return query.WorkPreparation{}, err
	}
	selection, err := decodeWorkRepositories(row.RepositorySelection)
	if err != nil {
		return query.WorkPreparation{}, err
	}
	checkouts, err := decodeRepositoryCheckouts(row.RepositoryCheckouts)
	if err != nil {
		return query.WorkPreparation{}, err
	}
	preparation := query.WorkPreparation{ThinkingLevel: row.ThinkingLevel, WorkID: row.ID, RequestID: row.ID, SubjectID: row.StartedBy,
		WorkBranch: selection.WorkBranch, Submitted: true, InputPrepared: len(checkouts) >= len(selection.Repositories),
		InputBuffered: initial == nil}
	for _, c := range command.PlanCheckouts(row.ID, selection) {
		preparation.Checkouts = append(preparation.Checkouts, query.PreparedCheckout{Provider: c.Provider, RepoID: c.RepoID, RepoName: c.RepoName, CloneURL: c.CloneURL, WebURL: c.WebURL,
			DefaultBranch: c.DefaultBranch, BaseBranch: c.BaseBranch, WorkBranch: c.WorkBranch, TargetPath: c.TargetPath, AccessID: c.AccessID})
	}
	if initial != nil {
		preparation.InputID, preparation.Input = initial.ID, initial.Text
	}
	return preparation, nil
}
