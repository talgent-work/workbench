package eventhandler

import (
	"context"
	"log/slog"
	"slices"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkAccessWithdrawal tells open message-stream readers of one Work that
// access ended: for the revoked reader, or, when the Work became private, for
// every reader other than the retained ones.
type WorkAccessWithdrawal struct {
	WorkID            string
	RevokedReaderID   string
	MadePrivate       bool
	RetainedReaderIDs []string
}

// WorkAccessWithdrawalPort delivers withdrawals to the message-stream readers.
type WorkAccessWithdrawalPort interface {
	PublishWorkAccessWithdrawal(context.Context, WorkAccessWithdrawal) error
}

// WorkAccessWithdrawals realizes "End revoked stream access" for
// WorkShareRevoked and WorkMadePrivate.
type WorkAccessWithdrawals struct {
	port   WorkAccessWithdrawalPort
	logger *slog.Logger
}

var _ domainevent.Handler = (*WorkAccessWithdrawals)(nil)

func NewWorkAccessWithdrawals(port WorkAccessWithdrawalPort, logger *slog.Logger) *WorkAccessWithdrawals {
	return &WorkAccessWithdrawals{port: port, logger: logger}
}

func (*WorkAccessWithdrawals) Listening() []domainevent.Kind {
	return []domainevent.Kind{domain.EventKindWorkShareRevoked, domain.EventKindWorkMadePrivate}
}

func (h *WorkAccessWithdrawals) Handle(ctx context.Context, event domainevent.Event) {
	var withdrawal WorkAccessWithdrawal
	switch fact := event.(type) {
	case domain.WorkShareRevoked:
		withdrawal = WorkAccessWithdrawal{WorkID: fact.WorkID, RevokedReaderID: fact.ReaderID}
	case domain.WorkMadePrivate:
		withdrawal = WorkAccessWithdrawal{WorkID: fact.WorkID, MadePrivate: true, RetainedReaderIDs: append(slices.Clone(fact.Owners), fact.ShareReaderIDs...)}
	default:
		return
	}
	// A lost withdrawal leaves an already open stream running until it
	// reconnects, where admission is decided again from current facts.
	if err := h.port.PublishWorkAccessWithdrawal(ctx, withdrawal); err != nil {
		h.logger.WarnContext(ctx, "publish Work access withdrawal failed", slog.String("agent_work_id", withdrawal.WorkID), slog.Any("error", err))
	}
}
