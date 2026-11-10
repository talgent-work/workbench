package marketplace_test

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/go-jimu/components/ddd/message"
	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace"
	"go.uber.org/fx"
	"xorm.io/xorm"
)

func TestModuleWiring(t *testing.T) {
	err := fx.ValidateApp(marketplace.Module, fx.NopLogger,
		fx.Supply(marketplace.Config{}, slog.Default(), http.NewServeMux()),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return nil }, fx.ResultTags(`name:"workbench"`))),
		fx.Provide(fx.Annotate(func() componenttaskqueue.Enqueuer { return nil }, fx.ResultTags(`name:"marketplace"`))),
		fx.Provide(fx.Annotate(func() *componenttaskqueue.SchemaRegistry { return nil }, fx.ResultTags(`name:"marketplace"`))),
		fx.Provide(func() message.Publisher { return nil }),
	)
	require.NoError(t, err)
}
