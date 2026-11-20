package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func inputsChange(work *domain.AgentWork) domain.InputChange {
	return domain.InputChange{ID: "change-1", Directory: work.LocatedLayout.Inputs.Directory, File: "brief.md", Operation: domain.FileAdded, At: time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC)}
}

// A Work without a File Home is told of each change in its located inputs
// until it closes; a change elsewhere is not its own.
func TestAWorkWithoutAFileHomeAdmitsNoticesOfItsInputChanges(t *testing.T) {
	work := standaloneWork(t)
	change := inputsChange(work.AgentWork)
	notice, err := work.AdmitInputChangeNotice(change)
	require.NoError(t, err)
	require.Equal(t, `Input file "brief.md" was added at 2026-11-20T08:30:00Z.`, notice.Text())
	require.Equal(t, "change-1", notice.Change)

	elsewhere := change
	elsewhere.Directory = work.LocatedLayout.PublicationTarget.Directory
	_, err = work.AdmitInputChangeNotice(elsewhere)
	require.ErrorIs(t, err, domain.ErrInputChangeNoticeNotAdmitted)

	for _, state := range []domain.WorkState{domain.WorkClosing, domain.WorkClosed} {
		work.State = state
		_, err = work.AdmitInputChangeNotice(change)
		require.ErrorIs(t, err, domain.ErrWorkTransition, state)
	}
}

// A Work with a File Home hears of its input changes from that File Home's
// owner, never from Agent.
func TestAWorkWithAFileHomeAdmitsNoInputChangeNotice(t *testing.T) {
	work := humanWork(t)
	work.FileHome = domain.FileHome{Kind: "case", ID: "1"}
	_, err := work.AdmitInputChangeNotice(inputsChange(work.AgentWork))
	require.ErrorIs(t, err, domain.ErrInputChangeNoticeNotAdmitted)
}

// The notice names the file, the operation and the time on one line; a
// rename names both names.
func TestAnInputChangeNoticeNamesOnlyTheFileTheOperationAndTheTime(t *testing.T) {
	at := time.Date(2026, 11, 20, 16, 30, 0, 0, time.FixedZone("CST", 8*3600))
	require.Equal(t, `Input file "b.pdf" was renamed to "c.pdf" at 2026-11-20T08:30:00Z.`, domain.InputChangeNotice{File: "c.pdf", PreviousFile: "b.pdf", Operation: domain.FileRenamed, At: at}.Text())
	require.Equal(t, `Input file "two\nlines.md" was deleted at 2026-11-20T08:30:00Z.`, domain.InputChangeNotice{File: "two\nlines.md", Operation: domain.FileDeleted, At: at}.Text())

	work := standaloneWork(t)
	for _, invalid := range []domain.InputChange{
		{Directory: work.LocatedLayout.Inputs.Directory, File: "a", Operation: domain.FileAdded, At: at},
		{ID: "c", Directory: work.LocatedLayout.Inputs.Directory, File: "a", Operation: "moved", At: at},
		{ID: "c", Directory: work.LocatedLayout.Inputs.Directory, File: "a", Operation: domain.FileRenamed, At: at},
		{ID: "c", Directory: work.LocatedLayout.Inputs.Directory, File: "a", Operation: domain.FileAdded},
	} {
		_, err := work.AdmitInputChangeNotice(invalid)
		require.ErrorIs(t, err, domain.ErrInvalidWork)
	}
}
