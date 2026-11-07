package messagehandler_test

import (
	"context"
	"testing"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	marketplaceintegrationv1 "github.com/talgent-work/contracts/gen/go/marketplace/integration/v1"
	storageintegrationv1 "github.com/talgent-work/contracts/gen/go/storage/integration/v1"
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
	require.Equal(t, []integrationmessage.Kind{"storage.integration.v1.FileChangedV1"}, handler.Listening())
	for _, fact := range []*storageintegrationv1.FileChangedV1{
		{Owner: "owner-1", Kind: storageintegrationv1.FileKind_FILE_KIND_ATTACHMENT, Name: "a.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_CREATED},
		{Owner: "owner-1", Kind: storageintegrationv1.FileKind_FILE_KIND_ATTACHMENT, Name: "b.pdf", PreviousName: "a.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_RENAMED},
		{Owner: "owner-2", Kind: storageintegrationv1.FileKind_FILE_KIND_ATTACHMENT, Name: "b.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_DELETED},
		{Owner: "owner-3", Kind: storageintegrationv1.FileKind_FILE_KIND_ARTIFACT, Name: "report.md", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_CREATED},
	} {
		msg, err := integrationmessage.New(integrationmessage.KindOf(fact), fact)
		require.NoError(t, err)
		require.NoError(t, handler.Handle(context.Background(), msg))
	}
	require.Equal(t, changedSpaces{"owner-1", "owner-1", "owner-2"}, *changed)

	other := &marketplaceintegrationv1.CredentialChangedV1{AuthorizationId: "authorization-1"}
	msg, err := integrationmessage.New(integrationmessage.KindOf(other), other)
	require.NoError(t, err)
	require.Error(t, handler.Handle(context.Background(), msg))
}
