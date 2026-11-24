//go:build integration

package database

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/go-jimu/components/transaction"
	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	drivermysql "github.com/go-sql-driver/mysql"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// Combining modules must not cause repositories to use another module's
// database. Exercise named production connections and private bindings against
// separate MySQL schemas: each context's repositories and transactions reach
// its own database only. The marker row goes into a table every leased schema
// has (marketplace_catalog_head), which the lease's reset empties again.
func TestModulesKeepDatabasesIndependent(t *testing.T) {
	databaseNames := make(map[string]string)
	dbs := make(map[string]*jimuxorm.DB)
	options := []fx.Option{fx.NopLogger, fx.Supply(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	for _, name := range []string{"storage", "execution", "sandbox"} {
		dsn := mysqltest.Database(t)
		parsed, err := drivermysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		databaseNames[name] = parsed.DBName
		options = append(options,
			fx.Supply(fx.Annotate(Option{DSN: parsed.FormatDSN()}, fx.ResultTags(`name:"`+name+`"`))),
			Module(name),
			fx.Module("business."+name, Bind(name), fx.Invoke(func(db *jimuxorm.DB, transactor transaction.Transactor) error {
				dbs[name] = db
				return transactor.Within(t.Context(), func(ctx context.Context) error {
					session, err := db.Session(ctx)
					if err != nil {
						return err
					}
					_, err = session.Exec("INSERT INTO marketplace_catalog_head(id, generation) VALUES (?, ?)", name, name)
					return err
				})
			})),
		)
	}
	app := fxtest.New(t, options...)
	app.RequireStart()
	for name, db := range dbs {
		session, err := db.Session(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		rows, err := session.QueryString("SELECT DATABASE() AS db, generation AS owner FROM marketplace_catalog_head")
		if err != nil {
			t.Fatalf("read %s marker: %v", name, err)
		}
		if len(rows) != 1 || rows[0]["db"] != databaseNames[name] || rows[0]["owner"] != name {
			t.Errorf("%s repository used the wrong database: %v", name, rows)
		}
	}
	app.RequireStop()
}
