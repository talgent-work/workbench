package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (a *Application) ResolveStartupCapabilities(ctx context.Context, user string, input domain.StartupCapabilityInput) (domain.CapabilitySnapshot, error) {
	return command.ResolveStartupCapabilities(ctx, a.Catalog, user, input)
}
