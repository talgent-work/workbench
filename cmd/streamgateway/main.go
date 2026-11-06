package main

import (
	"log/slog"
	"os"

	"github.com/go-jimu/components/config/loader"
	"github.com/go-jimu/components/sloghelper"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/streamgateway"
	pkgclickhouse "github.com/talgent-work/workbench/internal/pkg/clickhouse"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
	internalpkg "github.com/talgent-work/workbench/pkg"
	pkghttpserver "github.com/talgent-work/workbench/pkg/httpserver"
	"github.com/talgent-work/workbench/pkg/lifecyclelog"
)

// Option is the top-level configuration tree for the streamgateway service.
// Populated from configs/streamgateway/defaults.yml.
type Option struct {
	fx.Out

	Logger     sloghelper.Options        `json:"logger" toml:"logger" yaml:"logger"`
	HTTP       pkghttpserver.Config      `json:"http" toml:"http" yaml:"http"`
	ClickHouse pkgclickhouse.Config      `json:"clickhouse" toml:"clickhouse" yaml:"clickhouse"`
	NATS       pkgnats.Config            `json:"nats" toml:"nats" yaml:"nats"`
	Agent      streamgateway.AgentConfig `json:"agent" toml:"agent" yaml:"agent"`
}

func main() {
	var opt Option
	if err := loader.Load(&opt, loader.WithConfigurationDirectory("./configs/streamgateway", "defaults"), loader.WithConfigFilePrefix("streamgateway")); err != nil {
		slog.Error("failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	app := fx.New(
		lifecyclelog.Option,
		fx.Supply(opt),
		internalpkg.LoggerModule,
		internalpkg.HTTPModule,
		internalpkg.H2CClientModule,
		internalpkg.LazyClickHouseModule,
		internalpkg.NATSLazyModule,
		streamgateway.Module,
	)
	app.Run()
}
