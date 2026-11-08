package execution

import (
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"xorm.io/xorm"
)

// ExecutionQueryRepository is the execution query store: the listings and
// recorded outcomes Agent's persistence answers, and the credential and
// telemetry token issued to a Work's current live run, which only the
// execution realizations can decode from its specification.
type ExecutionQueryRepository struct {
	*infrastructure.ExecutionQueryRepository
	records records
}

var _ query.ExecutionQueryRepository = (*ExecutionQueryRepository)(nil)

func NewExecutionQueryRepository(db *xorm.Engine) *ExecutionQueryRepository {
	return &ExecutionQueryRepository{ExecutionQueryRepository: infrastructure.NewExecutionQueryRepository(db), records: records{db: db}}
}
