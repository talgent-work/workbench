//go:build integration

package infrastructure_test

import (
	"testing"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
)

func catalogDatabase(t *testing.T) *xorm.Engine {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}
