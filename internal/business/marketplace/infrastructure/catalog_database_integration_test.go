//go:build integration

package infrastructure_test

import (
	"testing"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
)

func catalogDatabase(t *testing.T) *jimuxorm.DB {
	t.Helper()
	_, db := catalogEngine(t)
	return db
}

// catalogEngine is the catalog database with the engine a test reads raw rows
// through.
func catalogEngine(t *testing.T) (*xorm.Engine, *jimuxorm.DB) {
	t.Helper()
	engine, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	db, err := jimuxorm.New(engine)
	require.NoError(t, err)
	return engine, db
}
