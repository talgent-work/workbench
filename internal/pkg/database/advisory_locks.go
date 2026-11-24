package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"

	"github.com/samber/oops"
)

// ErrLockHeld means another holder has the named lock now.
var ErrLockHeld = errors.New("database: advisory lock is held elsewhere")

// AdvisoryLocks holds MySQL named locks, which serialize work across
// replicas that no row guards. A named lock belongs to one physical
// connection, which a transaction Manager session never pins, so each hold
// borrows a connection of its own from the pool for the lock's life.
type AdvisoryLocks struct{ pool *sql.DB }

func NewAdvisoryLocks(pool *sql.DB) *AdvisoryLocks { return &AdvisoryLocks{pool: pool} }

// Hold runs fn while holding the lock named key, without waiting for it:
// another holder makes Hold return ErrLockHeld. MySQL bounds key at 64
// characters. Release uses its own deadline, so a canceled ctx never returns
// a connection to the pool still holding the lock.
func (l *AdvisoryLocks) Hold(ctx context.Context, key string, fn func() error) error {
	conn, err := l.pool.Conn(ctx)
	if err != nil {
		return oops.With("operation", "advisory_lock.connect").Wrap(err)
	}
	defer conn.Close()
	var acquired int
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", key).Scan(&acquired); err != nil {
		retire(conn)
		return oops.With("operation", "advisory_lock.acquire").Wrap(err)
	}
	if acquired != 1 {
		return ErrLockHeld
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var released int
		if err := conn.QueryRowContext(release, "SELECT RELEASE_LOCK(?)", key).Scan(&released); err != nil || released != 1 {
			retire(conn)
		}
	}()
	return fn()
}

// retire drops the physical connection, and with it every lock it holds.
func retire(conn *sql.Conn) { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
