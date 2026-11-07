//go:build integration

package mysqltest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Lease exclusively holds an initialized schema in the invocation's MySQL.
// Its dedicated advisory-lock connection fences other Go test processes.
type Lease struct {
	db         *sql.DB
	lock       *sql.Conn
	config     *mysql.Config
	name       string
	once       sync.Once
	releaseErr error
}

func Acquire(ctx context.Context) (*Lease, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	dsn, token := os.Getenv("TALGENT_TEST_MYSQL_DSN"), os.Getenv("TALGENT_TEST_MYSQL_TOKEN")
	if dsn == "" || token == "" {
		return nil, errors.New("run integration tests through scripts/test/with-middleware.sh")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil || config.DBName != "talgent_test_control" {
		return nil, errors.New("invalid isolated test MySQL configuration")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			conn.Close()
			db.Close()
		}
	}()
	var actual string
	var slots int
	if err := conn.QueryRowContext(ctx, "SELECT token, slots FROM test_run").Scan(&actual, &slots); err != nil {
		return nil, err
	}
	if actual != token || slots < 1 || slots > 99 {
		return nil, errors.New("test MySQL invocation marker mismatch")
	}
	for {
		for slot := 1; slot <= slots; slot++ {
			name := "talgent_test_" + strconv.Itoa(slot)
			var acquired int
			if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", name).Scan(&acquired); err != nil {
				return nil, err
			}
			if acquired == 1 {
				config.DBName = name
				lease := &Lease{db: db, lock: conn, config: config, name: name}
				failed = false
				return lease, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (l *Lease) ConnectionString(_ context.Context, args ...string) (string, error) {
	c := l.config.Clone()
	for _, arg := range args {
		values, err := url.ParseQuery(arg)
		if err != nil {
			return "", err
		}
		for key, values := range values {
			value := values[len(values)-1]
			switch key {
			case "parseTime":
				c.ParseTime = value == "true"
			case "multiStatements":
				c.MultiStatements = value == "true"
			default:
				if c.Params == nil {
					c.Params = map[string]string{}
				}
				c.Params[key] = value
			}
		}
	}
	return c.FormatDSN(), nil
}

// Reset removes fixture data without recreating tables or rerunning patches.
func (l *Lease) Reset(ctx context.Context) error {
	rows, err := l.lock.QueryContext(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema=? AND table_type='BASE TABLE' ORDER BY table_name", l.name)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var statement strings.Builder
	statement.WriteString("SET FOREIGN_KEY_CHECKS=0; START TRANSACTION;")
	for _, table := range tables {
		fmt.Fprintf(&statement, "DELETE FROM `%s`.`%s`;", l.name, strings.ReplaceAll(table, "`", "``"))
	}
	statement.WriteString("COMMIT; SET FOREIGN_KEY_CHECKS=1;")
	_, err = l.lock.ExecContext(ctx, statement.String())
	return err
}

func (l *Lease) Release(ctx context.Context) error {
	l.once.Do(func() {
		l.releaseErr = l.Reset(ctx)
		if l.releaseErr == nil {
			_, l.releaseErr = l.lock.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", l.name)
		}
		l.releaseErr = errors.Join(l.releaseErr, l.lock.Close(), l.db.Close())
	})
	return l.releaseErr
}
