// Package computer composes the Computer Bounded Context: every place a Work
// runs, the Clouds the platform operates and the Managed Computers people
// register, and the Desks their runs hold.
package computer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"

	"github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1/computerprivatev1connect"
	"github.com/talgent-work/workbench/gen/go/talgent/computer/public/v1/computerpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/application/messagepublisher"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
	"github.com/talgent-work/workbench/internal/business/computer/infrastructure"
	"github.com/talgent-work/workbench/internal/business/computer/infrastructure/cluster"
	"github.com/talgent-work/workbench/internal/business/computer/infrastructure/registration"
	transport "github.com/talgent-work/workbench/internal/business/computer/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/computerconnection"
	"github.com/talgent-work/workbench/internal/pkg/connectlog"
	pkgmysql "github.com/talgent-work/workbench/internal/pkg/mysql"
)

// Database is the Workbench core's MySQL engine, holding Computers and Desks.
const Database = "workbench"

// Config is Computer's trusted deployment configuration.
type Config struct {
	// ControlToken admits Agent to the admission contract.
	ControlToken string `json:"control-token" yaml:"control-token" toml:"control-token"`
	// Cloud is this cluster's Cloud.
	Cloud CloudConfig `json:"cloud" yaml:"cloud" toml:"cloud"`
	// VerificationURI is the browser page where a person approves a
	// registration's user code.
	VerificationURI string `json:"verification-uri" yaml:"verification-uri" toml:"verification-uri"`
	// MinimumVersion is the oldest Talgent Computer release, X.Y.Z, a Managed
	// Computer may take a run on; empty admits every release.
	MinimumVersion string `json:"minimum-version" yaml:"minimum-version" toml:"minimum-version"`
}

// minimumVersion is a platform release as Auto Tag numbers it, without its v.
var minimumVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// CloudConfig is this cluster's Cloud as the platform sets it.
type CloudConfig struct {
	// ID is the Cloud's fixed identity, a UUID; Works keep it for good.
	ID   string `json:"id" yaml:"id" toml:"id"`
	Name string `json:"name" yaml:"name" toml:"name"`
	// Capacity is the room the platform gives its runs: requests.cpu,
	// requests.memory and pods.
	Capacity map[string]string `json:"capacity" yaml:"capacity" toml:"capacity"`
	// Namespace and QuotaName locate the cluster's execution quota, in the
	// namespace where sandboxes run and which holds only them.
	Namespace string `json:"namespace" yaml:"namespace" toml:"namespace"`
	QuotaName string `json:"quota-name" yaml:"quota-name" toml:"quota-name"`
	// Demand is everything the quota charges for one run's Pod; its requests
	// are the room one run takes.
	Demand map[string]string `json:"demand" yaml:"demand" toml:"demand"`
}

func (c CloudConfig) cloud() (application.Cloud, corev1.ResourceList, error) {
	if uuid.Validate(c.ID) != nil || strings.TrimSpace(c.Name) == "" {
		return application.Cloud{}, nil, errors.New("computer: the Cloud needs a UUID identity and a name")
	}
	capacity, err := quantities(c.Capacity, "capacity")
	if err != nil {
		return application.Cloud{}, nil, err
	}
	demand, err := quantities(c.Demand, "demand")
	if err != nil {
		return application.Cloud{}, nil, err
	}
	pods := capacity[corev1.ResourcePods]
	return application.Cloud{
		ID: c.ID, Name: c.Name,
		Capacity:  room(capacity, pods.Value()),
		RunDemand: room(demand, 1),
	}, demand, nil
}

func quantities(raw map[string]string, what string) (corev1.ResourceList, error) {
	list := make(corev1.ResourceList, len(raw))
	for name, value := range raw {
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return nil, fmt.Errorf("computer: invalid Cloud %s %s: %w", what, name, err)
		}
		list[corev1.ResourceName(name)] = quantity
	}
	for _, key := range []corev1.ResourceName{corev1.ResourceRequestsCPU, corev1.ResourceRequestsMemory} {
		if quantity, ok := list[key]; !ok || quantity.Sign() <= 0 {
			return nil, fmt.Errorf("computer: positive Cloud %s %s is required", what, key)
		}
	}
	return list, nil
}

func room(list corev1.ResourceList, desks int64) domain.Room {
	cpu, memory := list[corev1.ResourceRequestsCPU], list[corev1.ResourceRequestsMemory]
	return domain.Room{MilliCPU: cpu.MilliValue(), MemoryBytes: memory.Value(), Desks: desks}
}

var Module = fx.Module("domain.computer",
	pkgmysql.Bind(Database),
	fx.Provide(
		fx.Private,
		func() func() time.Time { return time.Now },
		pkgmysql.NewExecutors,
		infrastructure.NewComputerRepository,
		infrastructure.NewDeskRepository,
		infrastructure.NewQueries,
		func(rdb *goredis.Client) *registration.Store { return registration.NewStore(rdb) },
		func(rdb *goredis.Client) *computerconnection.Reader { return computerconnection.NewReader(rdb) },
		newApplication,
	),
	fx.Invoke(ensureCloud, sweepPresence, registerHandlers),
)

type dependencies struct {
	fx.In
	Executors     *pkgmysql.Executors
	Computers     *infrastructure.ComputerRepository
	Desks         *infrastructure.DeskRepository
	Queries       *infrastructure.Queries
	Registrations *registration.Store
	Connections   *computerconnection.Reader
	Client        kubernetes.Interface
	// Bus publishes Desk Assigned and Presence changes; without it Agent
	// finds seated Desks and its Works' Disconnections only by asking.
	Bus       integrationmessage.Publisher `optional:"true"`
	Config    Config
	Now       func() time.Time
	Logger    *slog.Logger
	Lifecycle fx.Lifecycle
}

func newApplication(deps dependencies) (*application.Application, error) {
	config, client := deps.Config, deps.Client
	cloud, demand, err := config.Cloud.cloud()
	if err != nil {
		return nil, err
	}
	if config.MinimumVersion != "" && !minimumVersion.MatchString(config.MinimumVersion) {
		return nil, errors.New("computer: the minimum version must be X.Y.Z or empty")
	}
	if client == nil || strings.TrimSpace(config.Cloud.Namespace) == "" {
		return nil, errors.New("computer: the Cloud's Kubernetes client and namespace are required")
	}
	quota, err := cluster.NewQuota(client.CoreV1().ResourceQuotas(config.Cloud.Namespace), config.Cloud.QuotaName, demand)
	if err != nil {
		return nil, err
	}
	return application.NewApplication(application.Dependencies{
		Computers: deps.Computers, Desks: deps.Desks, Transactions: deps.Executors, Quota: quota, Queries: deps.Queries,
		Registrations: deps.Registrations, Connections: deps.Connections, Cloud: cloud, MinimumVersion: config.MinimumVersion, Now: deps.Now, Logger: deps.Logger,
		Seatings: seatings(deps.Lifecycle, deps.Bus, deps.Logger), Presences: presences(deps.Lifecycle, deps.Bus, deps.Logger),
		Removals: removals(deps.Lifecycle, deps.Bus, deps.Logger),
	}), nil
}

// removals publishes Computer Removed on the bus until the application
// stops, or not at all without one.
func removals(lc fx.Lifecycle, bus integrationmessage.Publisher, logger *slog.Logger) application.Removals {
	if bus == nil {
		return nil
	}
	publisher := messagepublisher.NewRemovals(bus, logger)
	lc.Append(fx.StopHook(publisher.Stop))
	return publisher
}

// seatings publishes on the bus until the application stops, or not at all
// without one.
func seatings(lc fx.Lifecycle, bus integrationmessage.Publisher, logger *slog.Logger) application.Seatings {
	if bus == nil {
		return nil
	}
	publisher := messagepublisher.NewSeatings(bus, logger)
	lc.Append(fx.StopHook(publisher.Stop))
	return publisher
}

// presences publishes Presence changes on the bus until the application
// stops, or not at all without one.
func presences(lc fx.Lifecycle, bus integrationmessage.Publisher, logger *slog.Logger) application.Presences {
	if bus == nil {
		return nil
	}
	publisher := messagepublisher.NewPresences(bus, logger)
	lc.Append(fx.StopHook(publisher.Stop))
	return publisher
}

// presenceSweep is how often a replica looks for Managed Computers whose
// connection ended without a report; any replica may sweep.
const presenceSweep = 30 * time.Second

// sweepPresence records the loss of reach a Gateway replica could not report.
func sweepPresence(lc fx.Lifecycle, app *application.Application, logger *slog.Logger) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				ticker := time.NewTicker(presenceSweep)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						if lost, err := app.SweepPresence(ctx); err != nil && ctx.Err() == nil {
							logger.WarnContext(ctx, "computer: presence sweep failed", "error", err, "lost", lost)
						}
					}
				}
			}()
			return nil
		},
		OnStop: func(stop context.Context) error {
			cancel()
			select {
			case <-done:
			case <-stop.Done():
			}
			return nil
		},
	})
}

// ensureCloud registers this cluster's Cloud before anything is admitted.
func ensureCloud(lc fx.Lifecycle, app *application.Application, logger *slog.Logger) {
	lc.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		return app.EnsureCloud(ctx, logger)
	}})
}

func registerHandlers(mux *http.ServeMux, app *application.Application, config Config, logger *slog.Logger) error {
	if strings.TrimSpace(config.ControlToken) == "" {
		return errors.New("computer: control token is required")
	}
	verification, err := url.Parse(config.VerificationURI)
	if err != nil || verification.Scheme != "https" && verification.Scheme != "http" || verification.Host == "" {
		return errors.New("computer: an absolute verification URI is required")
	}
	options := connect.WithInterceptors(connectlog.NewInterceptor(logger))
	mux.Handle(computerprivatev1connect.NewAdmissionServiceHandler(transport.NewAdmissionHandler(app, config.ControlToken), options))
	mux.Handle(computerprivatev1connect.NewConnectionServiceHandler(transport.NewConnectionHandler(app, config.ControlToken), options))
	mux.Handle(computerpublicv1connect.NewRegistrationServiceHandler(transport.NewRegistrationHandler(app, verification.String()), options))
	mux.Handle(computerpublicv1connect.NewComputerServiceHandler(transport.NewComputerHandler(app),
		connect.WithInterceptors(connectlog.NewInterceptor(logger), authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true}))))
	return nil
}
