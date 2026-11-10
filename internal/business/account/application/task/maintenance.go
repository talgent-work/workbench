// Package task holds Account's periodic tasks.
package task

import (
	"time"

	"github.com/go-jimu/components/taskqueue"
	"google.golang.org/protobuf/proto"

	accounttaskv1 "github.com/talgent-work/workbench/gen/account/task/v1"
)

// MaintenanceQueue is the lane Account rotates signing keys and forgets
// replaced refresh secrets on.
const MaintenanceQueue = "account-maintenance"

const (
	RotateSigningKeysType            taskqueue.TaskType = "account.signing-keys.rotate.v1"
	ForgetReplacedRefreshSecretsType taskqueue.TaskType = "account.refresh-secrets.forget.v1"
)

// NewRotateSigningKeys is the periodic task rotating signing keys every hour
// on the hour, UTC (Keys outlive their tokens). Every replica's scheduler
// fires it; uniqueness keeps one run per hour, and a failed run is replaced
// by the next.
func NewRotateSigningKeys() (taskqueue.PeriodicTask, error) {
	return periodic(RotateSigningKeysType, "0 * * * *", time.Hour, &accounttaskv1.RotateSigningKeysTaskV1{})
}

// NewForgetReplacedRefreshSecrets is the periodic task forgetting replaced
// refresh secrets every 24 hours at midnight, UTC.
func NewForgetReplacedRefreshSecrets() (taskqueue.PeriodicTask, error) {
	return periodic(ForgetReplacedRefreshSecretsType, "0 0 * * *", 24*time.Hour, &accounttaskv1.ForgetReplacedRefreshSecretsTaskV1{})
}

func periodic(kind taskqueue.TaskType, cron string, interval time.Duration, message proto.Message) (taskqueue.PeriodicTask, error) {
	schedule, err := taskqueue.CronSchedule(cron, taskqueue.WithLocation("UTC"))
	if err != nil {
		return taskqueue.PeriodicTask{}, err
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		return taskqueue.PeriodicTask{}, err
	}
	queued, err := taskqueue.New(taskqueue.Definition{Type: kind, Queue: MaintenanceQueue}, payload)
	if err != nil {
		return taskqueue.PeriodicTask{}, err
	}
	// A run is bounded well within its interval, so a stuck one never
	// overlaps the next.
	return taskqueue.NewPeriodicTask(string(kind), schedule, queued,
		taskqueue.WithMaxRetry(0), taskqueue.WithTimeout(10*time.Minute), taskqueue.WithUnique(interval*9/10))
}
