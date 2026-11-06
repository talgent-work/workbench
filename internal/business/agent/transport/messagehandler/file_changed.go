package messagehandler

import (
	"context"
	"fmt"

	integrationmessage "github.com/go-jimu/components/ddd/message"

	storageeventsv1 "github.com/talgent-work/workbench/gen/go/talgent/storage/events/v1"
)

// InputChanges follows a change of the Attachments in a File Space.
type InputChanges interface {
	InputsChanged(ctx context.Context, owner string) error
}

// FileChangedHandler consumes Storage's File Change Published Fact. Only an
// Attachment is a Work's input; an Artifact's change names no input, and
// Agent claims the space by its owner whatever the operation.
type FileChangedHandler struct {
	changes InputChanges
}

func NewFileChangedHandler(changes InputChanges) *FileChangedHandler {
	return &FileChangedHandler{changes: changes}
}

func (h *FileChangedHandler) Listening() []integrationmessage.Kind {
	return []integrationmessage.Kind{integrationmessage.KindOf(&storageeventsv1.FileChanged{})}
}

func (h *FileChangedHandler) Handle(ctx context.Context, msg integrationmessage.Message) error {
	fact, ok := msg.Payload().(*storageeventsv1.FileChanged)
	if !ok || fact == nil {
		return fmt.Errorf("agent file changed: unexpected payload %T", msg.Payload())
	}
	if fact.GetKind() != storageeventsv1.FileKind_FILE_KIND_ATTACHMENT {
		return nil
	}
	return h.changes.InputsChanged(ctx, fact.GetOwner())
}
