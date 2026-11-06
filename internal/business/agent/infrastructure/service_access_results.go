package infrastructure

import (
	"context"
	"encoding/json"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"xorm.io/xorm"
)

type ServiceAccessResults struct{ db *xorm.Engine }

var _ query.ServiceAccessReader = (*ServiceAccessResults)(nil)

func NewServiceAccessResults(db *xorm.Engine) *ServiceAccessResults {
	return &ServiceAccessResults{db: db}
}

func (r *ServiceAccessResults) ReadRepositoryPreparation(ctx context.Context, workID string) ([]query.RepositoryPreparation, error) {
	var row struct {
		Results []byte `xorm:"results"`
	}
	found, err := r.db.Context(ctx).SQL("SELECT JSON_EXTRACT(request_data, '$.CheckoutResults') AS results FROM agent_initial_execution_request WHERE id = ? AND deleted_at = 0", workID).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "read_repository_preparation").Wrap(err)
	}
	if !found || len(row.Results) == 0 {
		return nil, nil
	}
	var results []query.RepositoryPreparation
	if err := json.Unmarshal(row.Results, &results); err != nil {
		return nil, oops.With("operation", "decode_repository_preparation").Wrap(err)
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
