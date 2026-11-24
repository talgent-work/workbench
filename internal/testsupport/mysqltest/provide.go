//go:build integration

package mysqltest

import (
	"fmt"
	"testing"

	"github.com/go-jimu/components/transaction"
	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"xorm.io/xorm"
)

// Provide hands a test's engine to a process graph as the database named
// name, the way database.Module hands over one it opened: the engine, its
// go-jimu adapter and that adapter's transaction Manager. The test keeps
// owning the engine and closes it.
func Provide(t testing.TB, name string, engine *xorm.Engine) fx.Option {
	t.Helper()
	db, err := jimuxorm.New(engine)
	require.NoError(t, err)
	tag := fmt.Sprintf(`name:%q`, name)
	return fx.Provide(
		fx.Annotate(func() *xorm.Engine { return engine }, fx.ResultTags(tag)),
		fx.Annotate(func() *jimuxorm.DB { return db }, fx.ResultTags(tag)),
		fx.Annotate(func() transaction.Transactor { return db.Manager() }, fx.ResultTags(tag)),
	)
}
