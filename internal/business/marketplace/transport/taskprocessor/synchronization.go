package taskprocessor

import (
	"context"
	"fmt"

	"github.com/go-jimu/components/taskqueue"
	marketplacetaskv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/task/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	catalogtask "github.com/talgent-work/workbench/internal/business/marketplace/application/task"
	"google.golang.org/protobuf/proto"
)

func NewSynchronizationProcessor(app *application.CatalogSynchronization) taskqueue.Processor {
	return taskqueue.NewProcessor(catalogtask.SynchronizationType, func(ctx context.Context, task taskqueue.Task) error {
		payload := &marketplacetaskv1.SynchronizeCatalogTask{}
		if err := proto.Unmarshal(task.Payload(), payload); err != nil {
			return fmt.Errorf("%w: malformed synchronization task", taskqueue.ErrSkipRetry)
		}
		return app.Execute(ctx, payload.GetRunId())
	})
}
