package infrastructure

import (
	"slices"

	"github.com/samber/oops"
	"xorm.io/xorm"
)

// workShares reads a Work's Work Shares in grant order.
func workShares(db xorm.Interface, workID string) ([]string, error) {
	var readers []string
	err := db.Table("agent_work_share").Cols("reader_id").Where("work_id=?", workID).Asc("position").Find(&readers)
	if err != nil {
		return nil, oops.With("operation", "agent_work_share.list", "work_id", workID).Wrap(err)
	}
	return readers, nil
}

// retainWorkShares keeps a Work's Work Shares as its owned rows, rewriting
// them in the Work's transaction only when they changed.
func retainWorkShares(tx *xorm.Session, workID string, shares []string, now int64) error {
	current, err := workShares(tx, workID)
	if err != nil {
		return err
	}
	if slices.Equal(current, shares) || (len(current) == 0 && len(shares) == 0) {
		return nil
	}
	if _, err := tx.Where("work_id=?", workID).Delete(&agentWorkShareDO{}); err != nil {
		return oops.With("operation", "agent_work_share.replace", "work_id", workID).Wrap(err)
	}
	rows := make([]agentWorkShareDO, 0, len(shares))
	for i, reader := range shares {
		rows = append(rows, agentWorkShareDO{WorkID: workID, ReaderID: reader, Position: i, CreatedAt: now})
	}
	if len(rows) == 0 {
		return nil
	}
	_, err = tx.Insert(&rows)
	return oops.With("operation", "agent_work_share.replace", "work_id", workID).Wrap(err)
}
