package marketplace_test

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/go-jimu/components/ddd/message"
	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/go-jimu/components/transaction"
	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace"
	"github.com/talgent-work/workbench/internal/pkg/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"go.uber.org/fx"
	"xorm.io/xorm"
)

func TestModuleWiring(t *testing.T) {
	err := fx.ValidateApp(marketplace.Module, fx.NopLogger,
		fx.Supply(marketplace.Config{}, slog.Default(), &http.Client{}),
		fx.Provide(func() connectrpc.Server { return connecttest.NewServer() }),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return nil }, fx.ResultTags(`name:"workbench"`))),
		fx.Provide(fx.Annotate(func() *jimuxorm.DB { return nil }, fx.ResultTags(`name:"workbench"`))),
		fx.Provide(fx.Annotate(func() transaction.Transactor { return nil }, fx.ResultTags(`name:"workbench"`))),
		fx.Provide(fx.Annotate(func() componenttaskqueue.Enqueuer { return nil }, fx.ResultTags(`name:"marketplace"`))),
		fx.Provide(fx.Annotate(func() *componenttaskqueue.SchemaRegistry { return nil }, fx.ResultTags(`name:"marketplace"`))),
		fx.Provide(func() message.Publisher { return nil }),
	)
	require.NoError(t, err)
}
