package infrastructure

import (
	"context"
	"encoding/json"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"xorm.io/xorm"
)

type ServiceAccessResults struct{ db *xorm.Engine }

var _ query.ServiceAccessReader = (*ServiceAccessResults)(nil)

func NewServiceAccessResults(db *xorm.Engine) *ServiceAccessResults {
	return &ServiceAccessResults{db: db}
}

// ReadRepositoryPreparation reads the Work's recorded checkout outcomes in
// selection order, each at the target its selection plans for it.
func (r *ServiceAccessResults) ReadRepositoryPreparation(ctx context.Context, workID string) ([]query.RepositoryPreparation, error) {
	var row struct {
		RepositorySelection string `xorm:"repository_selection"`
		RepositoryCheckouts string `xorm:"repository_checkouts"`
	}
	found, err := r.db.Context(ctx).Table("agent_session").Cols("repository_selection", "repository_checkouts").Where("id = ? AND deleted_at = 0", workID).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "read_repository_preparation").Wrap(err)
	}
	if !found {
		return nil, nil
	}
	selection, err := decodeWorkRepositories(row.RepositorySelection)
	if err != nil {
		return nil, err
	}
	checkouts, err := decodeRepositoryCheckouts(row.RepositoryCheckouts)
	if err != nil {
		return nil, err
	}
	var results []query.RepositoryPreparation
	for _, planned := range command.PlanCheckouts(workID, selection) {
		for _, checkout := range checkouts {
			if checkout.RepositoryID == planned.RepoID {
				results = append(results, query.RepositoryPreparation{RepositoryID: checkout.RepositoryID, TargetPath: planned.TargetPath, Prepared: checkout.CheckedOut, Reason: checkout.Reason})
			}
		}
	}
	return results, nil
}

func (r *ServiceAccessResults) ReadServiceAccess(ctx context.Context, workID string) ([]query.ServiceAccessObservation, error) {
	var row struct {
		Observations []byte `xorm:"service_access_json"`
	}
	// Deliberately include retired allocations: a stopped Work retains its last
	// observations. A newer allocation's empty projection supersedes old results.
	found, err := r.db.Context(ctx).Table("execution_allocation").Cols("service_access_json").Where("session_id = ?", workID).Desc("created_at").Desc("id").Get(&row)
	if err != nil {
		return nil, oops.With("operation", "read_work_service_access").Wrap(err)
	}
	if !found {
		return nil, nil
	}
	var recorded []struct {
		Service string `json:"service"`
		Target  string `json:"target"`
		Plugin  string `json:"plugin"`
		MCP     string `json:"mcp"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(row.Observations, &recorded); err != nil {
		return nil, oops.With("operation", "decode_work_service_access").Wrap(err)
	}
	result := make([]query.ServiceAccessObservation, 0, len(recorded))
	for _, observation := range recorded {
		result = append(result, query.ServiceAccessObservation{Service: observation.Service, Target: observation.Target, Plugin: observation.Plugin, MCP: observation.MCP, Status: observation.Status, Reason: observation.Reason})
	}
	return result, nil
}
