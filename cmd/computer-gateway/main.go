// Command computer-gateway runs the Computer Gateway, which every Managed
// Computer's Talgent Computer connects out to.
package main

import (
	"log/slog"
	"os"

	"github.com/go-jimu/components/config/loader"
	"github.com/go-jimu/components/sloghelper"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/computergateway"
	internalpkg "github.com/talgent-work/workbench/pkg"
	pkghttpserver "github.com/talgent-work/workbench/pkg/httpserver"
	"github.com/talgent-work/workbench/pkg/lifecyclelog"
	pkgredis "github.com/talgent-work/workbench/pkg/redis"
)

// Option is the configuration tree, from configs/computer-gateway/defaults.yml.
type Option struct {
	fx.Out

	Logger  sloghelper.Options     `json:"logger" toml:"logger" yaml:"logger"`
	HTTP    pkghttpserver.Config   `json:"http" toml:"http" yaml:"http"`
	Redis   pkgredis.Config        `json:"redis" toml:"redis" yaml:"redis"`
	Gateway computergateway.Config `json:"gateway" toml:"gateway" yaml:"gateway"`
}

func main() {
	var opt Option
	if err := loader.Load(&opt, loader.WithConfigurationDirectory("./configs/computer-gateway", "defaults"), loader.WithConfigFilePrefix("computer-gateway")); err != nil {
		slog.Error("failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}
	fx.New(
		lifecyclelog.Option,
		fx.Supply(opt),
		internalpkg.LoggerModule,
		internalpkg.HTTPModule,
		internalpkg.H2CClientModule,
		internalpkg.RedisModule,
		computergateway.Module,
	).Run()
}
