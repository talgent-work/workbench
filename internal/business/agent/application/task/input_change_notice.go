package task

import (
	"strings"

	"github.com/go-jimu/components/taskqueue"
	"google.golang.org/protobuf/types/known/timestamppb"

	agenttaskv1 "github.com/talgent-work/workbench/gen/agent/task/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var fileOperations = map[domain.FileOperation]agenttaskv1.InputFileOperation{
	domain.FileAdded:    agenttaskv1.InputFileOperation_INPUT_FILE_OPERATION_ADDED,
	domain.FileReplaced: agenttaskv1.InputFileOperation_INPUT_FILE_OPERATION_REPLACED,
	domain.FileRenamed:  agenttaskv1.InputFileOperation_INPUT_FILE_OPERATION_RENAMED,
	domain.FileDeleted:  agenttaskv1.InputFileOperation_INPUT_FILE_OPERATION_DELETED,
}

var decodedOperations = func() map[agenttaskv1.InputFileOperation]domain.FileOperation {
	decoded := make(map[agenttaskv1.InputFileOperation]domain.FileOperation, len(fileOperations))
	for operation, encoded := range fileOperations {
		decoded[encoded] = operation
	}
	return decoded
}()

// NewDeliverInputChangeNotice queues the notice of one change to one Work,
// keyed by both, so a redelivered change collapses into the task already
// queued. A change that cannot be told is refused.
func NewDeliverInputChangeNotice(workID string, change domain.InputChange) (taskqueue.Task, error) {
	if err := change.Validate(); err != nil {
		return taskqueue.Task{}, err
	}
	if strings.TrimSpace(workID) == "" {
		return taskqueue.Task{}, taskqueue.ErrNilPayload
	}
	directory := &agenttaskv1.InputDirectoryV1{Name: change.Directory.Name}
	for _, fact := range change.Directory.Owner {
		directory.Owner = append(directory.Owner, &agenttaskv1.InputDirectoryV1_OwnerFact{Condition: string(fact.Condition), Value: fact.Value})
	}
	payload := &agenttaskv1.DeliverInputChangeNoticeTaskV1{WorkId: workID, ChangeId: change.ID, Directory: directory, File: change.File,
		PreviousFile: change.PreviousFile, Operation: fileOperations[change.Operation], OccurredAt: timestamppb.New(change.At)}
	return keyed(DeliverInputChangeNoticeType, RecoveryQueue, "input-change-notice", workID+":"+change.ID, payload)
}

// InputChangeOf is the change a queued notice tells of.
func InputChangeOf(payload *agenttaskv1.DeliverInputChangeNoticeTaskV1) domain.InputChange {
	change := domain.InputChange{ID: payload.GetChangeId(), Directory: domain.Directory{Name: payload.GetDirectory().GetName()},
		File: payload.GetFile(), PreviousFile: payload.GetPreviousFile()}
	for _, fact := range payload.GetDirectory().GetOwner() {
		change.Directory.Owner = append(change.Directory.Owner, domain.OwnerFact{Condition: domain.LocatorCondition(fact.GetCondition()), Value: fact.GetValue()})
	}
	change.Operation = decodedOperations[payload.GetOperation()]
	if payload.GetOccurredAt() != nil {
		change.At = payload.GetOccurredAt().AsTime()
	}
	return change
}
