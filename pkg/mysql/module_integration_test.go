//go:build integration

package mysql

import (
	"io"
	"log/slog"
	"testing"

	drivermysql "github.com/go-sql-driver/mysql"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/pkg/mysqltest"
)

// Combining modules must not cause repositories to use another module's
// database. Exercise named production connections and private bindings against
// separate MySQL schemas, including resource closure at process shutdown.
func TestModulesKeepDatabasesIndependent(t *testing.T) {
	databaseNames := make(map[string]string)
	engines := make(map[string]*xorm.Engine)
	options := []fx.Option{fx.NopLogger, fx.Supply(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	for _, name := range []string{"storage", "execution", "sandbox"} {
		dsn := mysqltest.Database(t)
		parsed, err := drivermysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		databaseNames[name] = parsed.DBName
		options = append(options,
			fx.Supply(fx.Annotate(Config{DSN: parsed.FormatDSN()}, fx.ResultTags(`name:"`+name+`"`))),
			Module(name),
			fx.Module("business."+name, Bind(name), fx.Invoke(func(engine *xorm.Engine) error {
				engines[name] = engine
				_, err := engine.Exec("INSERT INTO project(id, name) VALUES (?, ?)", name, name)
				return err
			})),
		)
	}
	app := fxtest.New(t, options...)
	app.RequireStart()
	for name, engine := range engines {
		rows, err := engine.QueryString("SELECT DATABASE() AS db, name AS owner FROM project")
		if err != nil {
			t.Fatalf("read %s marker: %v", name, err)
		}
		if len(rows) != 1 || rows[0]["db"] != databaseNames[name] || rows[0]["owner"] != name {
			t.Errorf("%s repository used the wrong database: %v", name, rows)
		}
	}
	app.RequireStop()
	for name, engine := range engines {
		if err := engine.Ping(); err == nil {
			t.Errorf("%s connection remained open after shutdown", name)
		}
	}
}
