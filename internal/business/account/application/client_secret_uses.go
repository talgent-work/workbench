package application

import (
	"context"
	"time"
)

// LastUseResolution is how precisely a Client Secret's last use is kept
// (Last use recorded): a grant within it of the recorded use moves nothing,
// so an Application that fetches tokens in parallel writes about once per
// resolution.
const LastUseResolution = time.Minute

// ClientSecretUses keeps each Client Secret's last use apart from its
// Application (Last use recorded): no Application decision reads it, so a
// grant that records one contends with no other grant and no owner change.
type ClientSecretUses interface {
	// RecordLastUse moves the secret's last use forward to at, unless the
	// recorded use is within resolution before at. It never moves it back.
	RecordLastUse(ctx context.Context, applicationID, secretID string, at time.Time, resolution time.Duration) error
}
