package dispatcher

import (
	"context"
	"log/slog"
	"net/http"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/messagedelivery"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/unifiedmessagelog"
	"github.com/talgent-work/workbench/internal/pkg/nats"
)

type OwnershipConfig = dispatcherredis.OwnershipConfig

type AgentFeedbackConfig struct {
	Addr  string `json:"addr" toml:"addr" yaml:"addr"`
	Token string `json:"token" toml:"token" yaml:"token"`
}

type InstanceID string

type GRPCAddr string

var Module = fx.Module("domain.dispatcher",
	nativeExecutionModule,
	fx.Provide(newOwnership),
	fx.Provide(fx.Annotate(dispatcherredis.NewInputBuffer, fx.As(fx.Self()), fx.As(new(command.InputBuffer)))),
	fx.Invoke(applyUnifiedMessageSchema),
	fx.Provide(newMessageDelivery),
	fx.Provide(func(client *http.Client, cfg AgentFeedbackConfig) command.ExecutionFeedbackReporter {
		return agentfeedback.NewClient(client, cfg.Addr, cfg.Token)
	}),
)

func applyUnifiedMessageSchema(lc fx.Lifecycle, conn chdriver.Conn) {
	lc.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		return unifiedmessagelog.ApplySchema(ctx, conn)
	}})
}

func newMessageDelivery(lc fx.Lifecycle, conn chdriver.Conn, nc *nats.LazyConn, logger *slog.Logger) (*messagedelivery.Sink, error) {
	sink, err := messagedelivery.NewSink(unifiedmessagelog.NewWriter(conn), messagedelivery.NewLiveNATSPublisher(nc), messagedelivery.NewDiagnosticNATSPublisher(nc), logger)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: sink.Close})
	return sink, nil
}

func newOwnership(rdb *goredis.Client, instanceID InstanceID, grpcAddr GRPCAddr, cfg OwnershipConfig) (*dispatcherredis.Ownership, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return dispatcherredis.NewOwnership(rdb, string(instanceID), string(grpcAddr), cfg.TTL.Duration(), cfg.SnapshotTTL.Duration()), nil
}
