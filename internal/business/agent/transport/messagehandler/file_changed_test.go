package messagehandler_test

import (
	"context"
	"testing"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketplaceintegrationv1 "github.com/talgent-work/contracts/gen/go/marketplace/integration/v1"
	storageintegrationv1 "github.com/talgent-work/contracts/gen/go/storage/integration/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
)

type changedInputs []domain.InputChange

func (c *changedInputs) InputsChanged(_ context.Context, change domain.InputChange) error {
	*c = append(*c, change)
	return nil
}

func directoryOf(condition, value, name string) *storageintegrationv1.Directory {
	return &storageintegrationv1.Directory{Owner: []*storageintegrationv1.OwnerFact{{Condition: condition, Value: value}}, Name: name}
}

// Every change in a Directory, whatever its operation, reaches the Works
// reading that Directory with its identity, file, operation and time; a
// change naming no Directory reaches none.
func TestFileChangesReachTheirDirectorysReaders(t *testing.T) {
	changed := &changedInputs{}
	at := time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC)
	handler := messagehandler.NewFileChangedHandler(changed)
	require.Equal(t, []integrationmessage.Kind{"storage.integration.v1.FileChangedV1"}, handler.Listening())
	for _, fact := range []*storageintegrationv1.FileChangedV1{
		{EventId: "e1", OccurredAt: timestamppb.New(at), Directory: directoryOf("file_home", "case:1", "inputs"), Name: "a.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_CREATED},
		{EventId: "e2", OccurredAt: timestamppb.New(at), Directory: directoryOf("file_home", "case:1", "inputs"), Name: "b.pdf", PreviousName: "a.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_RENAMED},
		{EventId: "e3", OccurredAt: timestamppb.New(at), Directory: directoryOf("work", "work-2", "inputs"), Name: "b.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_REPLACED},
		{EventId: "e4", OccurredAt: timestamppb.New(at), Directory: directoryOf("work", "work-2", "inputs"), Name: "b.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_DELETED},
		{Name: "report.md", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_CREATED},
	} {
		msg, err := integrationmessage.New(integrationmessage.KindOf(fact), fact)
		require.NoError(t, err)
		require.NoError(t, handler.Handle(context.Background(), msg))
	}
	caseInputs := domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionFileHome, Value: "case:1"}}, Name: "inputs"}
	workInputs := domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionWork, Value: "work-2"}}, Name: "inputs"}
	require.Equal(t, changedInputs{
		{ID: "e1", Directory: caseInputs, File: "a.pdf", Operation: domain.FileAdded, At: at},
		{ID: "e2", Directory: caseInputs, File: "b.pdf", PreviousFile: "a.pdf", Operation: domain.FileRenamed, At: at},
		{ID: "e3", Directory: workInputs, File: "b.pdf", Operation: domain.FileReplaced, At: at},
		{ID: "e4", Directory: workInputs, File: "b.pdf", Operation: domain.FileDeleted, At: at},
	}, *changed)

	// A change that names no operation or no time can never be told: the
	// consumer drops it rather than handling it again.
	for _, malformed := range []*storageintegrationv1.FileChangedV1{
		{EventId: "e5", OccurredAt: timestamppb.New(at), Directory: directoryOf("work", "work-2", "inputs"), Name: "b.pdf"},
		{EventId: "e6", Directory: directoryOf("work", "work-2", "inputs"), Name: "b.pdf", Operation: storageintegrationv1.FileOperation_FILE_OPERATION_CREATED},
	} {
		msg, err := integrationmessage.New(integrationmessage.KindOf(malformed), malformed)
		require.NoError(t, err)
		require.ErrorIs(t, handler.Handle(context.Background(), msg), integrationmessage.ErrPermanent)
	}
	require.Len(t, *changed, 4)

	other := &marketplaceintegrationv1.CredentialChangedV1{AuthorizationId: "authorization-1"}
	msg, err := integrationmessage.New(integrationmessage.KindOf(other), other)
	require.NoError(t, err)
	require.Error(t, handler.Handle(context.Background(), msg))
}
