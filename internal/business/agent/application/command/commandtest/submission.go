package commandtest

import (
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// CreationWaits are the creation waits a test handler counts each creation
// deadline with: shorter than any step a test takes, so an unanswered
// creation never holds a release unless the test is about its deadline.
var CreationWaits = domain.CreationWaits{Cloud: domain.CreationWait(time.Nanosecond), Managed: domain.CreationWait(time.Nanosecond)}

// Submit records the execution's creation as submitted when it was seated,
// or admitted while Queued, under CreationWaits, and reports whether it
// submitted. It panics on an invalid execution, which is a fixture defect.
func Submit(execution *domain.AgentExecution) bool {
	at := execution.Desk.SeatedAt
	if at.IsZero() {
		at = execution.StartAdmittedAt
	}
	if at.IsZero() {
		at = time.Now()
	}
	submitted, err := execution.RecordSubmission(at, CreationWaits)
	if err != nil {
		panic(err)
	}
	return submitted
}
