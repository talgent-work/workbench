package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/go-jimu/components/config/loader"
	"github.com/go-jimu/components/sloghelper"
	"github.com/talgent-work/workbench/internal/business/account"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/computer"
	"github.com/talgent-work/workbench/internal/business/marketplace"
	"github.com/talgent-work/workbench/internal/business/storage"
	"github.com/talgent-work/workbench/internal/business/trace"
	internalpkg "github.com/talgent-work/workbench/internal/pkg"
	pkgclickhouse "github.com/talgent-work/workbench/internal/pkg/clickhouse"
	pkghttpserver "github.com/talgent-work/workbench/internal/pkg/httpserver"
	pkgkafka "github.com/talgent-work/workbench/internal/pkg/kafka"
	pkgkubernetes "github.com/talgent-work/workbench/internal/pkg/kubernetes"
	"github.com/talgent-work/workbench/internal/pkg/lifecyclelog"
	pkgmysql "github.com/talgent-work/workbench/internal/pkg/mysql"
	"github.com/talgent-work/workbench/internal/pkg/nativeapi"
	"github.com/talgent-work/workbench/internal/pkg/nativeauth"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
	pkgredis "github.com/talgent-work/workbench/internal/pkg/redis"
	pkgtaskqueue "github.com/talgent-work/workbench/internal/pkg/taskqueue"
	"go.uber.org/fx"
)

// Option supplies the Workbench core's configuration tree while retaining
// module resource names. An extension runs in its own process.
type Option struct {
	NativeAPI              nativeapi.Config             `json:"native-api" yaml:"native-api" toml:"native-api"`
	AgentStorage           agent.StorageConfig          `json:"agent-storage" yaml:"agent-storage" toml:"agent-storage"`
	AgentAccount           agent.AccountConfig          `json:"agent-account" yaml:"agent-account" toml:"agent-account"`
	Storage                storage.Config               `json:"storage" yaml:"storage" toml:"storage"`
	NativeAuthentication   nativeauth.Config            `json:"native-authentication" yaml:"native-authentication" toml:"native-authentication"`
	AgentRPC               agent.RPCConfig              `json:"agent-rpc" yaml:"agent-rpc" toml:"agent-rpc"`
	AgentMarketplace       agent.PluginCatalogConfig    `json:"agent-marketplace" yaml:"agent-marketplace" toml:"agent-marketplace"`
	AgentComputer          agent.ComputerConfig         `json:"agent-computer" yaml:"agent-computer" toml:"agent-computer"`
	Computer               computer.Config              `json:"computer" yaml:"computer" toml:"computer"`
	AgentExecution         agent.ExecutionConfig        `json:"agent-execution" yaml:"agent-execution" toml:"agent-execution"`
	AgentRecovery          agent.RecoveryConfig         `json:"agent-recovery" yaml:"agent-recovery" toml:"agent-recovery"`
	AgentCredentialChanges agent.CredentialChangeConfig `json:"agent-credential-changes" yaml:"agent-credential-changes" toml:"agent-credential-changes"`
	AgentInputChanges      agent.InputChangeConfig      `json:"agent-input-changes" yaml:"agent-input-changes" toml:"agent-input-changes"`
	AgentDeskAssignments   agent.DeskAssignmentConfig   `json:"agent-desk-assignments" yaml:"agent-desk-assignments" toml:"agent-desk-assignments"`
	AgentComputerPresence  agent.ComputerPresenceConfig `json:"agent-computer-presence" yaml:"agent-computer-presence" toml:"agent-computer-presence"`
	fx.Out
	Logger         sloghelper.Options   `json:"logger" toml:"logger" yaml:"logger"`
	HTTP           pkghttpserver.Config `json:"http" toml:"http" yaml:"http"`
	ExecutionMySQL pkgmysql.Config      `name:"execution" json:"execution-mysql" toml:"execution-mysql" yaml:"execution-mysql"`
	Kafka          pkgkafka.Config      `json:"kafka" toml:"kafka" yaml:"kafka"`
	NATS           pkgnats.Config       `json:"nats" toml:"nats" yaml:"nats"`
	Redis          pkgredis.Config      `json:"redis" toml:"redis" yaml:"redis"`

	// WorkbenchMySQL holds Storage's and Marketplace's records.
	WorkbenchMySQL       pkgmysql.Config     `name:"workbench" json:"workbench-mysql" toml:"workbench-mysql" yaml:"workbench-mysql"`
	AccountMySQL         pkgmysql.Config     `name:"account" json:"account-mysql" toml:"account-mysql" yaml:"account-mysql"`
	MarketplaceTaskQueue pkgtaskqueue.Config `name:"marketplace" json:"marketplace-taskqueue" toml:"marketplace-taskqueue" yaml:"marketplace-taskqueue"`
	// Agent recovery runs on its own workers: preparation may hold a worker for
	// minutes, so it has its own lane and concurrency.
	AgentTaskQueue            pkgtaskqueue.Config  `name:"agent" json:"agent-taskqueue" toml:"agent-taskqueue" yaml:"agent-taskqueue"`
	AgentPreparationTaskQueue pkgtaskqueue.Config  `name:"agent-preparation" json:"agent-preparation-taskqueue" toml:"agent-preparation-taskqueue" yaml:"agent-preparation-taskqueue"`
	Marketplace               marketplace.Config   `json:"marketplace" toml:"marketplace" yaml:"marketplace"`
	Account                   account.Config       `json:"account" toml:"account" yaml:"account"`
	Kubernetes                pkgkubernetes.Config `json:"kubernetes" toml:"kubernetes" yaml:"kubernetes"`
	// Storage renders Preview Copies on its own lane; its concurrency is the
	// conversions one replica runs at once.
	StoragePreviewTaskQueue     pkgtaskqueue.Config `name:"storage-preview" json:"storage-preview-taskqueue" toml:"storage-preview-taskqueue" yaml:"storage-preview-taskqueue"`
	StorageMaintenanceTaskQueue pkgtaskqueue.Config `name:"storage-maintenance" json:"storage-maintenance-taskqueue" toml:"storage-maintenance-taskqueue" yaml:"storage-maintenance-taskqueue"`
	// Account rotates signing keys hourly and forgets replaced refresh
	// secrets daily.
	AccountMaintenanceTaskQueue pkgtaskqueue.Config `name:"account-maintenance" json:"account-maintenance-taskqueue" toml:"account-maintenance-taskqueue" yaml:"account-maintenance-taskqueue"`
	// ClickHouse holds Work message streams, read for Work turn results.
	ClickHouse pkgclickhouse.Config `json:"clickhouse" toml:"clickhouse" yaml:"clickhouse"`

	// Trace reads native telemetry from its own database and asks the Project
	// access decision and Agent's Work access through their contracts.
	TraceClickHouse trace.ClickHouseConfig `json:"trace-clickhouse" toml:"trace-clickhouse" yaml:"trace-clickhouse"`
	TraceAccess     trace.AccessConfig     `json:"trace-access" toml:"trace-access" yaml:"trace-access"`
}

func main() {
	var opt Option
	if err := loader.Load(&opt, loader.WithConfigurationDirectory("./configs/controlplane", "defaults"), loader.WithConfigFilePrefix("controlplane")); err != nil {
		slog.Error("failed to load controlplane configuration", slog.Any("error", err))
		os.Exit(1)
	}
	if err := validateOption(opt); err != nil {
		slog.Error("invalid controlplane configuration", slog.Any("error", err))
		os.Exit(1)
	}
	app := fx.New(appOptions(opt)...)
	// The fx event log is silenced; report why the process could not start.
	if err := app.Err(); err != nil {
		slog.Error("controlplane could not be assembled", slog.Any("error", err))
		os.Exit(1)
	}
	startCtx, cancel := context.WithTimeout(context.Background(), app.StartTimeout())
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		slog.Error("controlplane could not start", slog.Any("error", err))
		os.Exit(1)
	}
	// A module that cannot go on, such as a stopped Kafka consumer, shuts the
	// application down with a failure exit code so that the process restarts.
	shutdown := <-app.Wait()
	stopCtx, cancelStop := context.WithTimeout(context.Background(), app.StopTimeout())
	defer cancelStop()
	if err := app.Stop(stopCtx); err != nil {
		slog.Error("controlplane did not stop cleanly", slog.Any("error", err))
		os.Exit(1)
	}
	if shutdown.ExitCode != 0 {
		os.Exit(shutdown.ExitCode)
	}
}

func appOptions(opt Option) []fx.Option {
	return []fx.Option{
		nativeauth.Module,
		fx.Invoke(registerNativeAPI),
		lifecyclelog.Option,
		fx.StopTimeout(30 * time.Second),
		fx.Supply(opt),
		internalpkg.LoggerModule,
		internalpkg.MetricsModule("controlplane"),
		pkgmysql.Module(storage.Database),
		pkgmysql.Module("execution"),
		pkgmysql.Module(account.Database),
		internalpkg.RedisModule,
		internalpkg.H2CClientModule,
		internalpkg.KafkaProducerModule,
		internalpkg.KafkaConsumerModule,
		internalpkg.IntegrationPublisherModule,
		// Agent runs its own consumers of Marketplace's credential changes,
		// Storage's file changes, Computer's Desk seatings and Presence changes.
		internalpkg.IntegrationPayloadModule,
		internalpkg.NATSLazyModule,
		internalpkg.LazyClickHouseModule,
		account.Module,
		storage.Module,
		marketplace.Module,
		computer.Module,
		agent.Module,
		agent.ExecutionModule,
		agent.CredentialChangeModule,
		agent.InputChangeModule,
		agent.DeskAssignmentModule,
		agent.ComputerPresenceModule,
		agent.TurnResultModule,
		trace.Module,
		// Task lanes follow the business modules, so they stop first (fx
		// stops in reverse: HTTP, tasks, then messages).
		pkgtaskqueue.NamedModule(marketplace.Queue),
		pkgtaskqueue.NamedModule(storage.PreviewQueue),
		pkgtaskqueue.NamedModule(storage.MaintenanceQueue),
		pkgtaskqueue.NamedModule(account.MaintenanceQueue),
		pkgtaskqueue.NamedModule(agent.RecoveryQueue),
		pkgtaskqueue.NamedModule(agent.PreparationQueue),
		internalpkg.HTTPModule,
	}
}
