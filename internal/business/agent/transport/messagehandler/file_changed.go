package messagehandler

import (
	"context"
	"fmt"

	integrationmessage "github.com/go-jimu/components/ddd/message"

	storageintegrationv1 "github.com/talgent-work/contracts/gen/go/storage/integration/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// InputChanges follows a change of the files in a Directory.
type InputChanges interface {
	InputsChanged(ctx context.Context, directory domain.Directory) error
}

// FileChangedHandler consumes Storage's File Change Published Fact. Agent
// claims the Directory by its owner and name whatever the operation; only
// the Works reading it as their located inputs follow it.
type FileChangedHandler struct {
	changes InputChanges
}

func NewFileChangedHandler(changes InputChanges) *FileChangedHandler {
	return &FileChangedHandler{changes: changes}
}

func (h *FileChangedHandler) Listening() []integrationmessage.Kind {
	return []integrationmessage.Kind{integrationmessage.KindOf(&storageintegrationv1.FileChangedV1{})}
}

func (h *FileChangedHandler) Handle(ctx context.Context, msg integrationmessage.Message) error {
	fact, ok := msg.Payload().(*storageintegrationv1.FileChangedV1)
	if !ok || fact == nil {
		return fmt.Errorf("agent file changed: unexpected payload %T", msg.Payload())
	}
	directory := domain.Directory{Name: fact.GetDirectory().GetName()}
	for _, fact := range fact.GetDirectory().GetOwner() {
		directory.Owner = append(directory.Owner, domain.OwnerFact{Condition: domain.LocatorCondition(fact.GetCondition()), Value: fact.GetValue()})
	}
	if !directory.Present() || len(directory.Owner) == 0 {
		return nil
	}
	return h.changes.InputsChanged(ctx, directory)
}
