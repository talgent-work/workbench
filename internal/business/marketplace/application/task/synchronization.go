package task

import (
	"github.com/go-jimu/components/taskqueue"
	marketplacetaskv1 "github.com/talgent-work/workbench/gen/marketplace/task/v1"
	"google.golang.org/protobuf/proto"
)

const SynchronizationType taskqueue.TaskType = "marketplace.synchronize.v1"

// The repository's component v0.10.2 exposes raw envelopes; protobuf remains
// the durable payload contract until its native protobuf helpers are adopted.
func NewSynchronization(runID string) (taskqueue.Task, error) {
	payload, err := proto.Marshal(&marketplacetaskv1.SynchronizeCatalogTaskV1{RunId: runID})
	if err != nil {
		return taskqueue.Task{}, err
	}
	return taskqueue.New(taskqueue.Definition{Type: SynchronizationType, Queue: "marketplace-maintenance"}, payload)
}
