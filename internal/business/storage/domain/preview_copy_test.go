package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPreviewKey(t *testing.T) {
	_, err := NewPreviewKey(strings.Repeat("a", 64))
	require.NoError(t, err)
	for _, digest := range []string{"", "abc", strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		_, err := NewPreviewKey(digest)
		require.ErrorIs(t, err, ErrInvalidPreviewKey, digest)
	}
}

func rendering(t *testing.T, now time.Time) *PreviewCopy {
	t.Helper()
	key, err := NewPreviewKey(strings.Repeat("a", 64))
	require.NoError(t, err)
	return RequestRendering(key, now)
}

func TestARenderingSettlesOnceToACopyOrARecordedFailure(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	copied := rendering(t, now)
	require.Equal(t, PreviewRendering, copied.State)
	require.Equal(t, PreviewOutcome{}, copied.Outcome(), "a rendering in progress has no outcome")

	require.NoError(t, copied.Settle(RenderingSettlement{Copy: true}, now.Add(time.Minute)))
	require.Equal(t, PreviewRendered, copied.State)
	require.Equal(t, now.Add(time.Minute), copied.MadeAt)
	require.Equal(t, PreviewOutcome{Copy: true}, copied.Outcome())
	require.ErrorIs(t, copied.Settle(RenderingSettlement{Failure: PreviewRefusalCorrupt}, now), ErrPreviewSettled, "an outcome is settled once")
	require.ErrorIs(t, copied.Abandon(), ErrPreviewSettled)

	failed := rendering(t, now)
	require.NoError(t, failed.Settle(RenderingSettlement{Failure: PreviewRefusalEncrypted}, now))
	require.Equal(t, PreviewFailed, failed.State)
	require.Equal(t, PreviewOutcome{Failure: PreviewRefusalEncrypted}, failed.Outcome())

	temporary := rendering(t, now)
	require.ErrorIs(t, temporary.Settle(RenderingSettlement{}, now), ErrPreviewCopyNotFound, "a temporary failure settles nothing")
	require.Equal(t, PreviewRendering, temporary.State)
	require.NoError(t, temporary.Abandon(), "it is abandoned instead")
}

func TestPreviewCopiesLapseAfterRetention(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	settled := rendering(t, now)
	require.NoError(t, settled.Settle(RenderingSettlement{Copy: true}, now.Add(time.Hour)))
	require.False(t, settled.Lapsed(now.Add(24*time.Hour), 24*time.Hour), "lapse counts from when it was made")
	require.True(t, settled.Lapsed(now.Add(25*time.Hour), 24*time.Hour))

	stuck := rendering(t, now)
	require.False(t, stuck.RenderingAbandoned(now.Add(time.Minute), 90*time.Second))
	require.True(t, stuck.RenderingAbandoned(now.Add(90*time.Second), 90*time.Second), "past its deadline it is running no more")
	require.True(t, stuck.Lapsed(now.Add(24*time.Hour), 24*time.Hour))
	require.False(t, settled.RenderingAbandoned(now.Add(time.Hour*48), time.Second))
}
