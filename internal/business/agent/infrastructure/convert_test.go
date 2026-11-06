package infrastructure

import (
	"strings"
	"testing"
)

func TestInitialExecutionReceiptRoundTripsStoredIdentity(t *testing.T) {
	// Stored identity names are an explicit persistence contract; loading and
	// saving a receipt preserves the admitted input and its concurrency token.
	row := initialExecutionRequestDO{
		ID: "work-retained", RequestID: "startup-retained", Version: 7,
		RequestData: `{"SubjectID":"owner","SessionID":"work-retained","RequestID":"startup-retained","Input":{"ID":"input-retained","Text":"Keep the original task"},"Submitted":true}`,
	}
	request, err := initialExecutionRequestFromDO(row)
	if err != nil {
		t.Fatal(err)
	}
	if request.WorkID != row.ID || request.RequestID != row.RequestID || request.Input.ID != "input-retained" || !request.Submitted || request.Version != row.Version {
		t.Fatalf("retained startup identity or admission changed: %+v", request)
	}
	rewritten, err := initialExecutionRequestToDO(request)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.ID != row.ID || !strings.Contains(rewritten.RequestData, `"SessionID":"work-retained"`) || strings.Contains(rewritten.RequestData, `"WorkID":`) {
		t.Fatalf("stored identity key changed: %+v", rewritten)
	}
}
