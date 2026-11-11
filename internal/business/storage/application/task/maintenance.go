package task

import (
	"context"
	"math"
	"time"

	"github.com/go-jimu/components/taskqueue"
	"google.golang.org/protobuf/proto"

	storagetaskv1 "github.com/talgent-work/workbench/gen/storage/task/v1"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// MaintenanceQueue is the lane Storage removes reclaimed content and lapsed
// Preview Copies and repairs the Mapped View on, apart from the renderings
// that hold the preview lane.
const MaintenanceQueue = "storage-maintenance"

const (
	RemoveReclaimedContentType taskqueue.TaskType = "storage.directory.remove-reclaimed-content.v1"
	LapsePreviewCopiesType     taskqueue.TaskType = "storage.preview.lapse.v1"
	RepairMappedViewType       taskqueue.TaskType = "storage.mapped-view.repair.v1"
)

// RemoveReclaimedContent names the reclaimed Directory whose content goes.
type RemoveReclaimedContent struct {
	Directory domain.DirectoryID
}

// removalAttempt bounds one removal of a Directory's content.
const removalAttempt = 30 * time.Minute

// RequestContentRemoval enqueues the removal keyed by the Directory's owner
// and name, so a repeated reclamation is absorbed by the queued removal. It
// retries until the content is removed: a removal is never abandoned.
func RequestContentRemoval(ctx context.Context, enqueuer taskqueue.Enqueuer, removal RemoveReclaimedContent, key string) error {
	payload, err := proto.Marshal(&storagetaskv1.RemoveReclaimedContentTaskV1{Directory: directoryMessage(removal.Directory)})
	if err != nil {
		return err
	}
	queued, err := taskqueue.New(taskqueue.Definition{Type: RemoveReclaimedContentType, Queue: MaintenanceQueue}, payload,
		taskqueue.WithID(string(RemoveReclaimedContentType)+":"+key))
	if err != nil {
		return err
	}
	options := []taskqueue.EnqueueOption{taskqueue.WithMaxRetry(math.MaxInt32), taskqueue.WithTimeout(removalAttempt)}
	if err := taskqueue.NewEnqueueOptions(options...).Validate(); err != nil {
		return err
	}
	return enqueuer.Enqueue(ctx, queued, options...)
}

// ParseRemoveReclaimedContent reads a queued removal.
func ParseRemoveReclaimedContent(payload []byte) (RemoveReclaimedContent, error) {
	message := &storagetaskv1.RemoveReclaimedContentTaskV1{}
	if err := proto.Unmarshal(payload, message); err != nil {
		return RemoveReclaimedContent{}, err
	}
	directory, err := directoryID(message.GetDirectory())
	return RemoveReclaimedContent{Directory: directory}, err
}

// LapseInterval is how often lapsed Preview Copies are removed.
const LapseInterval = time.Hour

// NewLapsePreviewCopies is the periodic task removing lapsed Preview Copies
// and recorded failures every hour on the hour, UTC. Every replica's
// scheduler fires it; uniqueness keeps one run per hour, and a failed run is
// replaced by the next.
func NewLapsePreviewCopies() (taskqueue.PeriodicTask, error) {
	schedule, err := taskqueue.CronSchedule("0 * * * *", taskqueue.WithLocation("UTC"))
	if err != nil {
		return taskqueue.PeriodicTask{}, err
	}
	payload, err := proto.Marshal(&storagetaskv1.LapsePreviewCopiesTaskV1{})
	if err != nil {
		return taskqueue.PeriodicTask{}, err
	}
	queued, err := taskqueue.New(taskqueue.Definition{Type: LapsePreviewCopiesType, Queue: MaintenanceQueue}, payload)
	if err != nil {
		return taskqueue.PeriodicTask{}, err
	}
	return taskqueue.NewPeriodicTask(string(LapsePreviewCopiesType), schedule, queued,
		taskqueue.WithMaxRetry(0), taskqueue.WithTimeout(LapseInterval), taskqueue.WithUnique(LapseInterval*9/10))
}

// RepairInterval is how often the Mapped View is repaired.
const RepairInterval = time.Hour

// NewRepairMappedView is the periodic task repairing the Mapped View every
// hour at half past, UTC, apart from the lapse on the hour. Every replica's
// scheduler fires it; uniqueness keeps one run per hour, and a failed run is
// replaced by the next.
func NewRepairMappedView() (taskqueue.PeriodicTask, error) {
	schedule, err := taskqueue.CronSchedule("30 * * * *", taskqueue.WithLocation("UTC"))
	if err != nil {
		return taskqueue.PeriodicTask{}, err
	}
	payload, err := proto.Marshal(&storagetaskv1.RepairMappedViewTaskV1{})
	if err != nil {
		return taskqueue.PeriodicTask{}, err
	}
	queued, err := taskqueue.New(taskqueue.Definition{Type: RepairMappedViewType, Queue: MaintenanceQueue}, payload)
	if err != nil {
		return taskqueue.PeriodicTask{}, err
	}
	return taskqueue.NewPeriodicTask(string(RepairMappedViewType), schedule, queued,
		taskqueue.WithMaxRetry(0), taskqueue.WithTimeout(RepairInterval), taskqueue.WithUnique(RepairInterval*9/10))
}
