package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestWorkProgressReplacesThePlanWithoutProcessAttribution(t *testing.T) {
	work := newSession(t)
	ready(t, work)
	origin := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	first := []domain.WorkProgressItem{{Content: "Review the change", Status: "in_progress", ActiveForm: "Reviewing the change"}}
	require.NoError(t, work.RecordWorkProgress(10, first, origin))
	first[0].Content = "mutated caller input"
	require.Equal(t, "Review the change", work.Progress.Items[0].Content)

	completed := []domain.WorkProgressItem{{Content: "Run the regression", Status: "completed"}}
	require.NoError(t, work.RecordWorkProgress(20, completed, origin.Add(time.Minute)))
	require.Equal(t, completed, work.Progress.Items, "new reports replace the complete snapshot")
	require.NoError(t, work.RecordWorkProgress(20, completed, origin.Add(time.Hour)))
	require.Equal(t, origin.Add(time.Minute), work.Progress.UpdatedAt, "an exact replay retains the original update time")
	// An earlier position of the current process is a replay and changes nothing.
	replayed := []domain.WorkProgressItem{{Content: "Replayed plan", Status: "pending"}}
	require.NoError(t, work.RecordWorkProgress(15, replayed, origin.Add(90*time.Second)))
	require.Equal(t, completed, work.Progress.Items, "an earlier position is a replay")
	require.Equal(t, domain.WorkWorking, work.State, "a completed plan does not complete Work")
	require.NoError(t, work.RecordWorkProgress(30, nil, origin.Add(3*time.Minute)))
	require.Empty(t, work.Progress.Items, "an explicit empty plan replaces the old one")
}
