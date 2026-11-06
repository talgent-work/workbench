package messagehandler_test

import (
	"context"
	"testing"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	marketplaceeventsv1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/events/v1"
	storageeventsv1 "github.com/talgent-work/workbench/gen/go/talgent/storage/events/v1"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
)

type changedSpaces []string

func (c *changedSpaces) InputsChanged(_ context.Context, owner string) error {
	*c = append(*c, owner)
	return nil
}

// Every change of an Attachment, whatever its operation, wakes the inputs of
// the space's owner; an Artifact is no Work's input.
func TestAttachmentChangesWakeTheirSpacesInputs(t *testing.T) {
	changed := &changedSpaces{}
	handler := messagehandler.NewFileChangedHandler(changed)
	require.Equal(t, []integrationmessage.Kind{"talgent.storage.events.v1.FileChanged"}, handler.Listening())
	for _, fact := range []*storageeventsv1.FileChanged{
		{Owner: "owner-1", Kind: storageeventsv1.FileKind_FILE_KIND_ATTACHMENT, Name: "a.pdf", Operation: storageeventsv1.FileOperation_FILE_OPERATION_CREATED},
		{Owner: "owner-1", Kind: storageeventsv1.FileKind_FILE_KIND_ATTACHMENT, Name: "b.pdf", PreviousName: "a.pdf", Operation: storageeventsv1.FileOperation_FILE_OPERATION_RENAMED},
		{Owner: "owner-2", Kind: storageeventsv1.FileKind_FILE_KIND_ATTACHMENT, Name: "b.pdf", Operation: storageeventsv1.FileOperation_FILE_OPERATION_DELETED},
		{Owner: "owner-3", Kind: storageeventsv1.FileKind_FILE_KIND_ARTIFACT, Name: "report.md", Operation: storageeventsv1.FileOperation_FILE_OPERATION_CREATED},
	} {
		msg, err := integrationmessage.New(integrationmessage.KindOf(fact), fact)
		require.NoError(t, err)
		require.NoError(t, handler.Handle(context.Background(), msg))
	}
	require.Equal(t, changedSpaces{"owner-1", "owner-1", "owner-2"}, *changed)

	other := &marketplaceeventsv1.CredentialChanged{AuthorizationId: "authorization-1"}
	msg, err := integrationmessage.New(integrationmessage.KindOf(other), other)
	require.NoError(t, err)
	require.Error(t, handler.Handle(context.Background(), msg))
}
