package messagepublisher

import (
	"testing"
	"time"

	storageeventsv1 "github.com/talgent-work/workbench/gen/go/talgent/storage/events/v1"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// The published fact names the file, its change and who made it, and is
// identified by a fresh time-ordered event id.
func TestFileChangeMessageNamesTheChange(t *testing.T) {
	at := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	person, _ := domain.PersonWriter("user-1")
	work, _ := domain.WorkWriter("work-1")
	msg := fileChangeMessage(application.FileChange{
		Owner: "owner-1", Kind: domain.FileKindAttachment, Name: "final.md", PreviousName: "brief.md",
		Operation: application.FileRenamed, Writer: person, OccurredAt: at,
	})
	if msg.GetEventId() == "" || !msg.GetOccurredAt().AsTime().Equal(at) || msg.GetOwner() != "owner-1" ||
		msg.GetKind() != storageeventsv1.FileKind_FILE_KIND_ATTACHMENT || msg.GetName() != "final.md" || msg.GetPreviousName() != "brief.md" ||
		msg.GetOperation() != storageeventsv1.FileOperation_FILE_OPERATION_RENAMED || msg.GetActorUserId() != "user-1" || msg.GetActorWorkId() != "" {
		t.Fatalf("message = %v", msg)
	}
	artifact := fileChangeMessage(application.FileChange{Kind: domain.FileKindArtifact, Operation: application.FileDeleted, Writer: work, OccurredAt: at})
	if artifact.GetKind() != storageeventsv1.FileKind_FILE_KIND_ARTIFACT || artifact.GetOperation() != storageeventsv1.FileOperation_FILE_OPERATION_DELETED ||
		artifact.GetActorWorkId() != "work-1" || artifact.GetEventId() == msg.GetEventId() {
		t.Fatalf("artifact message = %v", artifact)
	}
}
