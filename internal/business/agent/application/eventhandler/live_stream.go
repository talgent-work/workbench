package eventhandler

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

// LiveStreamPort reaches the open live streams of a Work, which
// StreamGateway serves its readers: the Work's state as it is now, and the
// end of a reader's access to its message stream. A frame is best effort; a
// reader that missed one is brought up to date when its stream reconnects.
type LiveStreamPort interface {
	PublishWorkState(context.Context, query.WorkView) error
	PublishWorkAccessWithdrawal(context.Context, WorkAccessWithdrawal) error
}
