package storageerror

import (
	"errors"
	"testing"

	"connectrpc.com/connect/v2"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
)

// A refusal keeps its reason across the wire format of its details.
func TestReasonTravelsWithTheRefusal(t *testing.T) {
	refused := New(connect.CodeNotFound, storageprivatev1.ErrorReason_ERROR_REASON_SOURCE_NOT_FOUND, errors.New("missing"))
	wire := connect.NewError(refused.Code(), refused.Message()).WithRemote()
	for _, detail := range refused.Details() {
		wire = wire.WithDetail(detail)
	}
	if got := ReasonOf(wire); got != storageprivatev1.ErrorReason_ERROR_REASON_SOURCE_NOT_FOUND {
		t.Fatalf("reason = %v", got)
	}
	if got := ReasonOf(connect.NewError(connect.CodeNotFound, "plain")); got != storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED {
		t.Fatalf("plain reason = %v", got)
	}
}
