package messagehandler_test

import (
	"context"
	"testing"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"

	marketplaceintegrationv1 "github.com/talgent-work/contracts/gen/go/marketplace/integration/v1"
	storageintegrationv1 "github.com/talgent-work/contracts/gen/go/storage/integration/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
)

type changedDirectories []domain.Directory

func (c *changedDirectories) InputsChanged(_ context.Context, directory domain.Directory) error {
	*c = append(*c, directory)
	return nil
}

func directoryOf(condition, value, name string) *storageintegrationv1.Directory {
	return &storageintegrationv1.Directory{Owner: []*storageintegrationv1.OwnerFact{{Condition: condition, Value: value}}, Name: name}
}

// Every change in a Directory, whatever its operation, wakes the Works
// reading that Directory; a change naming no Directory wakes none.
func TestFileChangesWakeTheirDirectorysReaders(t *testing.T) {
	changed := &changedDirectories{}
	handler := messagehandler.NewFileChangedHandler(changed)
	require.Equal(t, []integrationmessage.Kind{"storage.integration.v1.FileChangedV1"}, handler.Listening())
	for _, fact := range []*storageintegrationv1.FileChangedV1{
		{Directory: directoryOf("file_home", "case:1", "inputs"), Name: "a.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_CREATED},
		{Directory: directoryOf("file_home", "case:1", "inputs"), Name: "b.pdf", PreviousName: "a.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_RENAMED},
		{Directory: directoryOf("work", "work-2", "inputs"), Name: "b.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_DELETED},
		{Name: "report.md", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_CREATED},
	} {
		msg, err := integrationmessage.New(integrationmessage.KindOf(fact), fact)
		require.NoError(t, err)
		require.NoError(t, handler.Handle(context.Background(), msg))
	}
	caseInputs := domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionFileHome, Value: "case:1"}}, Name: "inputs"}
	workInputs := domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionWork, Value: "work-2"}}, Name: "inputs"}
	require.Equal(t, changedDirectories{caseInputs, caseInputs, workInputs}, *changed)

	other := &marketplaceintegrationv1.CredentialChangedV1{AuthorizationId: "authorization-1"}
	msg, err := integrationmessage.New(integrationmessage.KindOf(other), other)
	require.NoError(t, err)
	require.Error(t, handler.Handle(context.Background(), msg))
}
