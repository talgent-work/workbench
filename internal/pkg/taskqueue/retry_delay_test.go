package taskqueue

import (
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

func TestRetryDelayDoublesFromTheMinimumUpToTheMaximum(t *testing.T) {
	delay := retryDelay(Config{RetryDelayMin: Duration(time.Second), RetryDelayMax: Duration(time.Minute)})
	for attempt, want := range map[int]time.Duration{0: time.Second, 1: 2 * time.Second, 3: 8 * time.Second, 6: time.Minute, 1000: time.Minute} {
		got := delay(attempt, nil, nil)
		if got > want || got < want-want/4 {
			t.Fatalf("attempt %d delay = %v, want within a quarter below %v", attempt, got, want)
		}
	}
}

func TestRetryDelayKeepsTheProviderDefaultWithoutARange(t *testing.T) {
	task := asynq.NewTask("taskqueue.retry.v1", nil)
	if got := retryDelay(Config{})(5, nil, task); got <= 0 {
		t.Fatalf("default delay = %v", got)
	}
}

func TestRetryDelayRangeMustBeOrderedAndComplete(t *testing.T) {
	for name, cfg := range map[string]Config{
		"minimum only": {RedisAddr: "redis:6379", RetryDelayMin: Duration(time.Second)},
		"inverted":     {RedisAddr: "redis:6379", RetryDelayMin: Duration(time.Minute), RetryDelayMax: Duration(time.Second)},
	} {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s: invalid retry delay range accepted", name)
		}
	}
	if err := (Config{RedisAddr: "redis:6379", RetryDelayMin: Duration(time.Second), RetryDelayMax: Duration(time.Minute)}).Validate(); err != nil {
		t.Fatal(err)
	}
}
