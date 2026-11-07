package command

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// SandboxMountPort reaches Storage's Sandbox Mount Contract: it obtains the
// Mount Grants of a Cloud start, and Storage decides whether each space is
// created or reused and where it lives.
type SandboxMountPort interface {
	AcquireMounts(ctx context.Context, request domain.SandboxMountRequest) (domain.SandboxMounts, error)
}
