//go:build integration

package execution

import (
	"context"

	"xorm.io/xorm"
)

// latestAllocation returns the Work's most recent allocation, live or retired.
func latestAllocation(ctx context.Context, db *xorm.Engine, workID string) (executionRecord, bool, error) {
	var record executionRecord
	found, err := db.Context(ctx).Where("session_id = ?", workID).Desc("created_at").Desc("id").Get(&record)
	return record, found, err
}
