// Package taskprocessor runs Account's periodic tasks.
package taskprocessor

import (
	"context"

	"github.com/go-jimu/components/taskqueue"

	applicationtask "github.com/talgent-work/workbench/internal/business/account/application/task"
)

// Maintenance is the use cases the periodic tasks delegate to.
type Maintenance interface {
	RotateSigningKeys(ctx context.Context) error
	ForgetReplacedRefreshSecrets(ctx context.Context) error
}

// processor runs one periodic task; a failed run is replaced by the next.
type processor struct {
	kind taskqueue.TaskType
	run  func(context.Context) error
}

var _ taskqueue.Processor = processor{}

func (p processor) TaskType() taskqueue.TaskType { return p.kind }

func (p processor) Process(ctx context.Context, _ taskqueue.Task) error { return p.run(ctx) }

// NewMaintenanceProcessors are the processors of Account's periodic tasks.
func NewMaintenanceProcessors(app Maintenance) []taskqueue.Processor {
	return []taskqueue.Processor{
		processor{kind: applicationtask.RotateSigningKeysType, run: app.RotateSigningKeys},
		processor{kind: applicationtask.ForgetReplacedRefreshSecretsType, run: app.ForgetReplacedRefreshSecrets},
	}
}
