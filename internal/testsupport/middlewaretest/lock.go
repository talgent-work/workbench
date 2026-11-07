package middlewaretest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"
)

// The invocation token isolates local concurrent runs; a process crash releases
// its advisory file lock. The runner and tests share one host filesystem.
func Lock(t *testing.T, name string) func() {
	t.Helper()
	token := Environment(t, "TALGENT_TEST_MYSQL_TOKEN")
	lock := flock.New(filepath.Join(os.TempDir(), "talgent-test-"+token+"-"+name+".lock"))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	acquired, err := lock.TryLockContext(ctx, 20*time.Millisecond)
	require.NoError(t, err)
	require.True(t, acquired)
	var once sync.Once
	return func() { once.Do(func() { require.NoError(t, lock.Unlock()); require.NoError(t, lock.Close()) }) }
}

// LeaseSlot holds one of slots interchangeable resources named name, such as a
// logical database of the shared Redis, until the returned release runs;
// callers wait only when every slot is held.
func LeaseSlot(t *testing.T, name string, slots int) (int, func()) {
	t.Helper()
	token := Environment(t, "TALGENT_TEST_MYSQL_TOKEN")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	for {
		for slot := range slots {
			lock := flock.New(filepath.Join(os.TempDir(), fmt.Sprintf("talgent-test-%s-%s-%d.lock", token, name, slot)))
			acquired, err := lock.TryLock()
			require.NoError(t, err)
			if acquired {
				var once sync.Once
				return slot, func() { once.Do(func() { require.NoError(t, lock.Unlock()); require.NoError(t, lock.Close()) }) }
			}
			require.NoError(t, lock.Close())
		}
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), "every %s slot stayed held", name)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
