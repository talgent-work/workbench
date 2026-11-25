package main

import (
	"log/slog"
	"os"

	"github.com/go-jimu/components/config/loader"
	"github.com/go-jimu/components/sloghelper"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/streamgateway"
	internalpkg "github.com/talgent-work/workbench/internal/pkg"
	pkgclickhouse "github.com/talgent-work/workbench/internal/pkg/clickhouse"
	"github.com/talgent-work/workbench/internal/pkg/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/lifecyclelog"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
	"github.com/talgent-work/workbench/internal/pkg/telemetry"
)

// Option is the top-level configuration tree for the streamgateway service.
// Populated from configs/streamgateway/defaults.yml.
type Option struct {
	fx.Out

	Logger     sloghelper.Options        `json:"logger" toml:"logger" yaml:"logger"`
	HTTP       connectrpc.Option         `json:"http" toml:"http" yaml:"http"`
	ClickHouse pkgclickhouse.Config      `json:"clickhouse" toml:"clickhouse" yaml:"clickhouse"`
	NATS       pkgnats.Config            `json:"nats" toml:"nats" yaml:"nats"`
	Agent      streamgateway.AgentConfig `json:"agent" toml:"agent" yaml:"agent"`
	Live       streamgateway.LiveConfig  `json:"live" toml:"live" yaml:"live"`
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
		telemetry.Module("streamgateway"),
		connectrpc.Module,
		internalpkg.LazyClickHouseModule,
		internalpkg.NATSLazyModule,
		streamgateway.Module,
		internalpkg.Lifecycle,
	)
	app.Run()
}
