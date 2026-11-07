// Package messagepublisher publishes Storage's Integration Messages.
package messagepublisher

import (
	"context"
	"log/slog"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	storageintegrationv1 "github.com/talgent-work/contracts/gen/go/storage/integration/v1"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

const fileChangePublishTimeout = 5 * time.Second

// FileChangePublisher publishes the File Change Published Fact, best effort
// and keyed by the space's owner. Publishing never delays or fails
// the change: it runs after the request, so one owner's changes may reach the bus
// out of order, and consumers order them by their time-ordered event ids.
type FileChangePublisher struct {
	bus     integrationmessage.Publisher
	logger  *slog.Logger
	timeout time.Duration
}

var _ application.FileChangesPort = (*FileChangePublisher)(nil)

func NewFileChangePublisher(bus integrationmessage.Publisher, logger *slog.Logger) *FileChangePublisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &FileChangePublisher{bus: bus, logger: logger.With("component", "storage.file_change_publisher"), timeout: fileChangePublishTimeout}
}

func (p *FileChangePublisher) Publish(parent context.Context, change application.FileChange) {
	if p == nil || p.bus == nil {
		return
	}
	payload := fileChangeMessage(change)
	attrs := []any{slog.String("owner", change.Owner), slog.String("operation", string(change.Operation)), slog.String("event_id", payload.GetEventId())}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), p.timeout)
		defer cancel()
		msg, err := integrationmessage.New(integrationmessage.KindOf(payload), payload, integrationmessage.WithKey(change.Owner))
		if err == nil {
			err = p.bus.Publish(ctx, msg)
		}
		if err != nil {
			p.logger.WarnContext(ctx, "publish file change failed", append(attrs, slog.Any("error", err))...)
			return
		}
		p.logger.InfoContext(ctx, "file change published", attrs...)
	}()
}

// fileChangeMessage maps a committed file change to its published fact.
func fileChangeMessage(change application.FileChange) *storageintegrationv1.FileChangedV1 {
	return &storageintegrationv1.FileChangedV1{
		EventId:      uuid.Must(uuid.NewV7()).String(),
		OccurredAt:   timestamppb.New(change.OccurredAt),
		Owner:        change.Owner,
		Kind:         fileKinds[change.Kind],
		Name:         change.Name,
		PreviousName: change.PreviousName,
		Operation:    fileOperations[change.Operation],
		ActorUserId:  change.Writer.UserID(),
		ActorWorkId:  change.Writer.WorkID(),
	}
}

var fileKinds = map[domain.FileKind]storageintegrationv1.FileKind{
	domain.FileKindAttachment: storageintegrationv1.FileKind_FILE_KIND_ATTACHMENT,
	domain.FileKindArtifact:   storageintegrationv1.FileKind_FILE_KIND_ARTIFACT,
}

var fileOperations = map[application.FileOperation]storageintegrationv1.FileOperation{
	application.FileCreated:  storageintegrationv1.FileOperation_FILE_OPERATION_CREATED,
	application.FileReplaced: storageintegrationv1.FileOperation_FILE_OPERATION_REPLACED,
	application.FileRenamed:  storageintegrationv1.FileOperation_FILE_OPERATION_RENAMED,
	application.FileDeleted:  storageintegrationv1.FileOperation_FILE_OPERATION_DELETED,
}
