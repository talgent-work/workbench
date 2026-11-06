package taskprocessor

import (
	"context"
	"errors"
	"testing"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"

	scmapp "github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/tokenmaintenance"
)

type fakeRefreshAuthorizationProcessor struct {
	payloads []tokenmaintenance.RefreshAuthorizationPayload
	err      error
}

func (p *fakeRefreshAuthorizationProcessor) Process(_ context.Context, payload tokenmaintenance.RefreshAuthorizationPayload) error {
	p.payloads = append(p.payloads, payload)
	return p.err
}

// Authorization refresh processors must use the registry payload schema and
// map permanent provider failures to ErrSkipRetry so the queue does not retry
// a terminal condition.
func TestRefreshAuthorizationProcessor_DecodesPayloadAndMapsPermanentFailure(t *testing.T) {
	registry := componenttaskqueue.NewSchemaRegistry()
	cfg, err := tokenmaintenance.Config{}.Normalize()
	if err != nil {
		t.Fatalf("normalize cfg: %v", err)
	}
	if err := RegisterTaskSchemas(registry, cfg); err != nil {
		t.Fatalf("register schemas: %v", err)
	}
	inner := &fakeRefreshAuthorizationProcessor{
		err: scmapp.NewProviderError("revoked", false, "authorization revoked", errors.New("revoked")),
	}
	processor := NewRefreshAuthorizationTaskProcessor(registry, inner)
	task, err := registry.NewJSONTask(&tokenmaintenance.RefreshAuthorizationPayload{
		AuthorizationID: "auth-1",
		Reason:          tokenmaintenance.ReasonAccessExpiring,
	})
	if err != nil {
		t.Fatalf("new json task: %v", err)
	}

	err = processor.Process(context.Background(), task)
	if !errors.Is(err, componenttaskqueue.ErrSkipRetry) {
		t.Fatalf("error = %v, want ErrSkipRetry", err)
	}
	if len(inner.payloads) != 1 || inner.payloads[0].AuthorizationID != "auth-1" {
		t.Fatalf("payloads = %+v", inner.payloads)
	}
}
