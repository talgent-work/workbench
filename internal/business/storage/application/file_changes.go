package application

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	storageintegrationv1 "github.com/talgent-work/contracts/gen/go/storage/integration/v1"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// FileOperation is how a file Storage wrote changed.
type FileOperation string

const (
	FileCreated  FileOperation = "created"
	FileReplaced FileOperation = "replaced"
	FileRenamed  FileOperation = "renamed"
	FileDeleted  FileOperation = "deleted"
)

// FileChange is one committed change of a file Storage carried out: its
// Directory, its writer and which file, never its content.
type FileChange struct {
	Directory domain.DirectoryID
	// Name is the file's name after the change; PreviousName is the former
	// name of a renamed file.
	Name, PreviousName string
	Operation          FileOperation
	Writer             domain.FileWriter
}

// fileChangePublishTimeout bounds one best-effort publication.
const fileChangePublishTimeout = 5 * time.Second

// fileChanged publishes the File Change Published Fact of a committed
// change, best effort and keyed by its Directory. Publishing never delays or
// fails the change: it runs after the request, so one Directory's changes may
// reach the bus out of order, and consumers order them by their time-ordered
// event ids.
func (a *Application) fileChanged(parent context.Context, change FileChange) {
	if a.publisher == nil {
		return
	}
	payload := fileChangeMessage(change, a.now().UTC())
	key := directoryKey(change.Directory)
	attrs := []any{slog.String("directory", key), slog.String("operation", string(change.Operation)), slog.String("event_id", payload.GetEventId())}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), fileChangePublishTimeout)
		defer cancel()
		msg, err := message.New(message.KindOf(payload), payload, message.WithKey(key))
		if err == nil {
			err = a.publisher.Publish(ctx, msg)
		}
		if err != nil {
			a.logger.WarnContext(ctx, "publish file change failed", append(attrs, slog.Any("error", err))...)
			return
		}
		a.logger.InfoContext(ctx, "file change published", attrs...)
	}()
}

// fileChangeMessage maps a committed file change to its published fact.
func fileChangeMessage(change FileChange, occurredAt time.Time) *storageintegrationv1.FileChangedV1 {
	directory := &storageintegrationv1.Directory{Name: change.Directory.Name}
	for _, fact := range change.Directory.Owner.Facts() {
		directory.Owner = append(directory.Owner, &storageintegrationv1.OwnerFact{Condition: fact.Condition, Value: fact.Value})
	}
	return &storageintegrationv1.FileChangedV1{
		EventId:            uuid.Must(uuid.NewV7()).String(),
		OccurredAt:         timestamppb.New(occurredAt),
		Directory:          directory,
		Name:               change.Name,
		PreviousName:       change.PreviousName,
		Operation:          fileOperations[change.Operation],
		ActorUserId:        change.Writer.UserID(),
		ActorWorkId:        change.Writer.WorkID(),
		ActorApplicationId: change.Writer.ApplicationID(),
	}
}

var fileOperations = map[FileOperation]storageintegrationv1.FileOperation{
	FileCreated:  storageintegrationv1.FileOperation_FILE_OPERATION_CREATED,
	FileReplaced: storageintegrationv1.FileOperation_FILE_OPERATION_REPLACED,
	FileRenamed:  storageintegrationv1.FileOperation_FILE_OPERATION_RENAMED,
	FileDeleted:  storageintegrationv1.FileOperation_FILE_OPERATION_DELETED,
}

// directoryKey names a Directory in one unambiguous string: its owner's
// facts as condition=value joined by ",", then "/" and its name, each part
// escaped. It keys the Directory's messages and tasks.
func directoryKey(id domain.DirectoryID) string {
	facts := id.Owner.Facts()
	written := make([]string, 0, len(facts))
	for _, fact := range facts {
		written = append(written, url.QueryEscape(fact.Condition)+"="+url.QueryEscape(fact.Value))
	}
	return strings.Join(written, ",") + "/" + url.QueryEscape(id.Name)
}

// writtenOperation is the operation a committed write was.
func writtenOperation(replace bool) FileOperation {
	if replace {
		return FileReplaced
	}
	return FileCreated
}
