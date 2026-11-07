package main

import (
	"log/slog"
	"os"

	"github.com/go-jimu/components/config/loader"
	"github.com/go-jimu/components/sloghelper"
	"github.com/google/uuid"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/dispatcher"
	internalpkg "github.com/talgent-work/workbench/internal/pkg"
	pkgclickhouse "github.com/talgent-work/workbench/internal/pkg/clickhouse"
	pkghttpserver "github.com/talgent-work/workbench/internal/pkg/httpserver"
	pkgintegrationmessage "github.com/talgent-work/workbench/internal/pkg/integrationmessage"
	pkgkafka "github.com/talgent-work/workbench/internal/pkg/kafka"
	"github.com/talgent-work/workbench/internal/pkg/lifecyclelog"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
	pkgredis "github.com/talgent-work/workbench/internal/pkg/redis"
)

// Option is the top-level configuration tree for the dispatcher service.
type Option struct {
	fx.Out
	AgentFeedback   dispatcher.AgentFeedbackConfig   `json:"agent-feedback" toml:"agent-feedback" yaml:"agent-feedback"`
	NativeExecution dispatcher.NativeExecutionConfig `json:"native-execution" toml:"native-execution" yaml:"native-execution"`

	Logger              sloghelper.Options           `json:"logger" toml:"logger" yaml:"logger"`
	HTTP                pkghttpserver.Config         `json:"http" toml:"http" yaml:"http"`
	Redis               pkgredis.Config              `json:"redis" toml:"redis" yaml:"redis"`
	ClickHouse          pkgclickhouse.Config         `json:"clickhouse" toml:"clickhouse" yaml:"clickhouse"`
	Kafka               pkgkafka.Config              `json:"kafka" toml:"kafka" yaml:"kafka"`
	IntegrationMessages pkgintegrationmessage.Config `json:"integration-messages" toml:"integration-messages" yaml:"integration-messages"`
	NATS                pkgnats.Config               `json:"nats" toml:"nats" yaml:"nats"`
	Ownership           dispatcher.OwnershipConfig   `json:"ownership" toml:"ownership" yaml:"ownership"`
	InstanceID          dispatcher.InstanceID        `json:"instance_id" toml:"instance_id" yaml:"instance_id"`
	GRPCAddr            dispatcher.GRPCAddr          `json:"grpc_addr" toml:"grpc_addr" yaml:"grpc_addr"`
}

func main() {
	var opt Option
	if err := loader.Load(&opt, loader.WithConfigurationDirectory("./configs/dispatcher", "defaults"), loader.WithConfigFilePrefix("dispatcher")); err != nil {
		slog.Error("failed to load configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}
	if opt.InstanceID == "" {
		opt.InstanceID = dispatcher.InstanceID("disp-" + uuid.Must(uuid.NewV7()).String()[:8])
	}

	app := fx.New(
		lifecyclelog.Option,
		fx.Supply(opt),
		internalpkg.LoggerModule,
		internalpkg.HTTPModule,
		internalpkg.RedisModule,
		internalpkg.KafkaProducerModule,
		internalpkg.IntegrationPublisherModule,
		internalpkg.ClickHouseModule,
		internalpkg.NATSLazyModule,
		internalpkg.H2CClientModule,
		dispatcher.Module,
	)
	app.Run()
}
