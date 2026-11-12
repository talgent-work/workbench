package infrastructure

import (
	"github.com/samber/oops"
	"xorm.io/builder"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// agentWorkLabelDO is one of a Work's Work Labels. A Work's labels are its
// owned rows, written once with the Work, and their own table so a listing
// narrows Works by a label through an index.
type agentWorkLabelDO struct {
	WorkID     string `xorm:"work_id pk"`
	LabelKey   string `xorm:"label_key pk"`
	LabelValue string `xorm:"label_value"`
	CreatedAt  int64  `xorm:"created_at"`
}

func (agentWorkLabelDO) TableName() string { return "agent_work_label" }

// workLabels reads a Work's Work Labels.
func workLabels(db xorm.Interface, workID string) (domain.WorkLabels, error) {
	var rows []agentWorkLabelDO
	if err := db.Table("agent_work_label").Cols("label_key", "label_value").Where("work_id=?", workID).Find(&rows); err != nil {
		return domain.WorkLabels{}, oops.With("operation", "agent_work_label.list", "work_id", workID).Wrap(err)
	}
	pairs := make(map[string]string, len(rows))
	for _, row := range rows {
		pairs[row.LabelKey] = row.LabelValue
	}
	return domain.NewWorkLabels(pairs)
}

// insertWorkLabels writes a new Work's Work Labels in its creating
// transaction; a Work keeps them for good, so nothing rewrites them.
func insertWorkLabels(tx *xorm.Session, workID string, labels domain.WorkLabels, now int64) error {
	pairs := labels.Pairs()
	if len(pairs) == 0 {
		return nil
	}
	rows := make([]agentWorkLabelDO, 0, len(pairs))
	for _, pair := range pairs {
		rows = append(rows, agentWorkLabelDO{WorkID: workID, LabelKey: pair.Key, LabelValue: pair.Value, CreatedAt: now})
	}
	_, err := tx.Insert(&rows)
	return oops.With("operation", "agent_work_label.insert", "work_id", workID).Wrap(err)
}

// carryingWorkLabels narrows Works, aliased s, to those carrying every one of
// the labels.
func carryingWorkLabels(labels []domain.WorkLabel) builder.Cond {
	condition := builder.NewCond()
	for _, pair := range labels {
		condition = condition.And(builder.Expr("s.id IN (SELECT l.work_id FROM agent_work_label l WHERE l.label_key=? AND l.label_value=?)", pair.Key, pair.Value))
	}
	return condition
}

// listedWorkLabels reads the Work Labels of listed Works by Work; a Work
// without labels is absent.
func listedWorkLabels(db xorm.Interface, workIDs []string) (map[string]map[string]string, error) {
	if len(workIDs) == 0 {
		return nil, nil
	}
	var rows []agentWorkLabelDO
	if err := db.Table("agent_work_label").Cols("work_id", "label_key", "label_value").In("work_id", workIDs).Find(&rows); err != nil {
		return nil, oops.With("operation", "agent_work_label.list_listed").Wrap(err)
	}
	labels := make(map[string]map[string]string)
	for _, row := range rows {
		if labels[row.WorkID] == nil {
			labels[row.WorkID] = make(map[string]string)
		}
		labels[row.WorkID][row.LabelKey] = row.LabelValue
	}
	return labels, nil
}
