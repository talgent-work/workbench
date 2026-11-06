package account

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/account/application"
)

const maintenanceInterval = time.Minute

// registerMaintenance provisions the first signing key before the process
// serves, then keeps keys rotated and forgets expired reuse evidence. Every
// replica runs it; both steps are idempotent across replicas.
func registerMaintenance(lc fx.Lifecycle, app *application.Application, logger *slog.Logger) {
	var cancel context.CancelFunc
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := app.RotateSigningKeys(ctx); err != nil {
				return fmt.Errorf("account signing keys: %w", err)
			}
			loop, stop := context.WithCancel(context.Background())
			cancel = stop
			go func() {
				defer close(done)
				ticker := time.NewTicker(maintenanceInterval)
				defer ticker.Stop()
				for {
					select {
					case <-loop.Done():
						return
					case <-ticker.C:
					}
					step, release := context.WithTimeout(loop, 30*time.Second)
					if err := app.RotateSigningKeys(step); err != nil && loop.Err() == nil {
						logger.Warn("account signing key rotation will retry", slog.Any("error", err))
					}
					if err := app.ForgetExpiredRefreshSecrets(step); err != nil && loop.Err() == nil {
						logger.Warn("account refresh secret retention will retry", slog.Any("error", err))
					}
					release()
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel == nil {
				return nil
			}
			cancel()
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
