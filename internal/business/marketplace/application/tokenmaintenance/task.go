package tokenmaintenance

import (
	"hash/fnv"
	"time"

	componenttaskqueue "github.com/go-jimu/components/taskqueue"
)

const (
	TaskTypeRefreshScan          componenttaskqueue.TaskType = "scm.github_authorization_refresh_scan.v1"
	TaskTypeRefreshAuthorization componenttaskqueue.TaskType = "scm.github_authorization_refresh.v1"
)

type RefreshScanPayload struct{}

func RegisterTaskSchemas(registry *componenttaskqueue.SchemaRegistry, cfg Config) error {
	normalized, err := cfg.Normalize()
	if err != nil {
		return err
	}
	for _, registration := range []struct {
		def     componenttaskqueue.Definition
		factory func() any
	}{
		{def: taskDefinition(TaskTypeRefreshScan, normalized.QueueName), factory: func() any { return &RefreshScanPayload{} }},
		{def: taskDefinition(TaskTypeRefreshAuthorization, normalized.QueueName), factory: func() any { return &RefreshAuthorizationPayload{} }},
	} {
		if err := registry.Register(registration.def, registration.factory); err != nil {
			return err
		}
	}
	return nil
}

func taskDefinition(taskType componenttaskqueue.TaskType, queue string) componenttaskqueue.Definition {
	return componenttaskqueue.Definition{Type: taskType, Queue: queue}
}

func AuthorizationTaskEnqueueOptions(authorizationID string, cfg Config) []componenttaskqueue.EnqueueOption {
	jitter := StableJitter(authorizationID, cfg.ScanInterval.Duration())
	return []componenttaskqueue.EnqueueOption{
		componenttaskqueue.WithDelay(jitter),
		componenttaskqueue.WithMaxRetry(5),
		componenttaskqueue.WithTimeout(5 * time.Minute),
		componenttaskqueue.WithUnique(cfg.ScanInterval.Duration() + jitter),
	}
}

func RefreshScanUniqueTTL(cfg Config) time.Duration {
	interval := cfg.ScanInterval.Duration()
	if interval <= 0 {
		interval = defaultScanInterval
	}
	unique := interval - time.Second
	if unique < time.Second {
		return time.Second
	}
	return unique
}

func StableJitter(key string, window time.Duration) time.Duration {
	if window <= 0 {
		return 0
	}
	if window > time.Minute {
		window = time.Minute
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return time.Duration(h.Sum64() % uint64(window))
}
