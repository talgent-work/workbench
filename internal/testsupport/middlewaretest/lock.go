package middlewaretest

import (
	"context"
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
