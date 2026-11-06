package marketplace

import (
	"encoding/json"
	"testing"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/tokenmaintenance"
)

// The SCM maintenance scan periodic producer must use a provider-neutral
// component PeriodicTask and keep duplicate suppression on scheduled fires.
func TestNewRefreshScanPeriodicTask_UsesMaintenanceTaskDefinition(t *testing.T) {
	cfg := tokenmaintenance.Config{
		ScanInterval:           tokenmaintenance.Duration(2 * time.Minute),
		ProactiveRefreshWindow: tokenmaintenance.Duration(30 * time.Minute),
		RequestRefreshSkew:     tokenmaintenance.Duration(5 * time.Minute),
		RefreshLeaseDuration:   tokenmaintenance.Duration(30 * time.Second),
		BatchSize:              100,
		QueueName:              "scm-maintenance",
	}
	registry := componenttaskqueue.NewSchemaRegistry()
	if err := tokenmaintenance.RegisterTaskSchemas(registry, cfg); err != nil {
		t.Fatalf("register task schemas: %v", err)
	}

	periodicTasks, err := newRefreshScanPeriodicTasks(maintenanceTaskSchemasRegistered{}, cfg, registry)
	if err != nil {
		t.Fatalf("new periodic tasks: %v", err)
	}
	if len(periodicTasks) != 1 {
		t.Fatalf("periodic tasks len = %d, want 1", len(periodicTasks))
	}
	periodic := periodicTasks[0]
	if periodic.Name() != "scm.github_authorization_refresh_scan" {
		t.Fatalf("name = %q, want scm.github_authorization_refresh_scan", periodic.Name())
	}
	if periodic.Schedule().Kind() != componenttaskqueue.ScheduleKindInterval {
		t.Fatalf("schedule kind = %q, want interval", periodic.Schedule().Kind())
	}
	if periodic.Schedule().Interval() != 2*time.Minute {
		t.Fatalf("interval = %s, want 2m", periodic.Schedule().Interval())
	}
	if periodic.Task().Type() != tokenmaintenance.TaskTypeRefreshScan {
		t.Fatalf("task type = %q, want %q", periodic.Task().Type(), tokenmaintenance.TaskTypeRefreshScan)
	}
	if periodic.Task().Queue() != cfg.QueueName {
		t.Fatalf("queue = %q, want %q", periodic.Task().Queue(), cfg.QueueName)
	}
	options := periodic.EnqueuePolicy()
	if got, want := options.UniqueTTL(), tokenmaintenance.RefreshScanUniqueTTL(cfg); got != want {
		t.Fatalf("unique ttl = %s, want %s", got, want)
	}
}

// A disabled SCM maintenance scan must be omitted from DI registration instead
// of represented by a false Enabled flag on a periodic task contract.
func TestNewRefreshScanPeriodicTasks_DisabledMaintenanceOmitsProducer(t *testing.T) {
	cfg := disabledMaintenanceConfig(t)
	registry := componenttaskqueue.NewSchemaRegistry()
	if err := tokenmaintenance.RegisterTaskSchemas(registry, cfg); err != nil {
		t.Fatalf("register task schemas: %v", err)
	}

	periodicTasks, err := newRefreshScanPeriodicTasks(maintenanceTaskSchemasRegistered{}, cfg, registry)
	if err != nil {
		t.Fatalf("new periodic tasks: %v", err)
	}
	if len(periodicTasks) != 0 {
		t.Fatalf("periodic tasks len = %d, want 0", len(periodicTasks))
	}
}

func disabledMaintenanceConfig(t *testing.T) tokenmaintenance.Config {
	t.Helper()
	var cfg tokenmaintenance.Config
	if err := json.Unmarshal([]byte(`{"enabled": false}`), &cfg); err != nil {
		t.Fatalf("unmarshal disabled config: %v", err)
	}
	return cfg
}
