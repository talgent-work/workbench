package domain

import (
	"encoding/hex"
	"errors"
	"time"
)

var (
	// ErrInvalidPreviewKey means a Preview Copy key is not a content digest.
	ErrInvalidPreviewKey = errors.New("storage: a preview copy is keyed by a SHA-256 content digest")
	// ErrPreviewCopyNotFound means a content has no Preview Copy, no recorded
	// failure and no rendering in progress.
	ErrPreviewCopyNotFound = errors.New("storage: no preview copy for the content")
	// ErrPreviewSettled means the rendering's outcome was already settled.
	ErrPreviewSettled = errors.New("storage: the preview copy's outcome is settled")
)

// PreviewKey identifies the Preview Copy, or the recorded failure, of one
// content: its SHA-256 digest alone. Keyed by content rather than position,
// a copy never shows a replaced file's old content as the new one, and files
// holding the same content share it.
type PreviewKey struct {
	Digest string
}

func NewPreviewKey(digest string) (PreviewKey, error) {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != digest {
		return PreviewKey{}, ErrInvalidPreviewKey
	}
	return PreviewKey{Digest: digest}, nil
}

// PreviewCopyState is a Preview Copy's lifecycle.
type PreviewCopyState string

const (
	// PreviewRendering: a rendering was requested and has no outcome yet;
	// another opening requests none.
	PreviewRendering PreviewCopyState = "rendering"
	// PreviewRendered: its Preview Copy is shown to readers.
	PreviewRendered PreviewCopyState = "rendered"
	// PreviewFailed: its recorded failure is shown instead of a rendering.
	PreviewFailed PreviewCopyState = "failed"
)

// PreviewCopy is the rendering outcome of one content, keyed by its digest,
// from the opening that requested it until it lapses. Files with identical
// content share it, and no File position owns it.
type PreviewCopy struct {
	Key   PreviewKey
	State PreviewCopyState
	// Failure is the recorded failure's reason, in PreviewFailed.
	Failure PreviewRefusal
	// RequestedAt is when its rendering was requested.
	RequestedAt time.Time
	// MadeAt is when its outcome was settled, from which its lapse counts.
	MadeAt time.Time
}

// RequestRendering establishes the Preview Copy of a content an admitted
// opening found without an outcome, entering Rendering.
func RequestRendering(key PreviewKey, now time.Time) *PreviewCopy {
	return &PreviewCopy{Key: key, State: PreviewRendering, RequestedAt: now.UTC()}
}

// RenderingAbandoned reports whether a rendering requested longer ago than
// its deadline can no longer be running, so it holds no content back from a
// new request.
func (p *PreviewCopy) RenderingAbandoned(now time.Time, deadline time.Duration) bool {
	return p.State == PreviewRendering && !now.Before(p.RequestedAt.Add(deadline))
}

// Settle records the outcome Preview Policy Settle rendering decided,
// transitioning from Rendering to Rendered or Failed. A temporary failure
// settles nothing: the copy is abandoned instead.
func (p *PreviewCopy) Settle(settled RenderingSettlement, now time.Time) error {
	if p.State != PreviewRendering {
		return ErrPreviewSettled
	}
	switch {
	case settled.Copy:
		p.State = PreviewRendered
	case settled.Failure != PreviewRefusalNone:
		p.State, p.Failure = PreviewFailed, settled.Failure
	default:
		return ErrPreviewCopyNotFound
	}
	p.MadeAt = now.UTC()
	return nil
}

// Abandon admits removing a rendering that failed only temporarily, so the
// next opening requests it again.
func (p *PreviewCopy) Abandon() error {
	if p.State != PreviewRendering {
		return ErrPreviewSettled
	}
	return nil
}

// Lapsed reports whether its outcome is older than the retention period, so
// it is removed and a later opening renders the content again. A rendering
// lapses once it is that old too.
func (p *PreviewCopy) Lapsed(now time.Time, retention time.Duration) bool {
	since := p.MadeAt
	if p.State == PreviewRendering {
		since = p.RequestedAt
	}
	return !now.Before(since.Add(retention))
}

// Outcome is what Preview Policy reads of it: its Preview Copy, its
// recorded failure, or nothing while rendering.
func (p *PreviewCopy) Outcome() PreviewOutcome {
	switch p.State {
	case PreviewRendered:
		return PreviewOutcome{Copy: true}
	case PreviewFailed:
		return PreviewOutcome{Failure: p.Failure}
	}
	return PreviewOutcome{}
}
