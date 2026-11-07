// Package computergateway is the Computer Gateway: the public endpoint every
// Managed Computer's Talgent Computer connects out to. It holds
// each machine's one connection, records in Redis which replica holds it,
// and reports reach to Computer. It holds no data of its own and is not a
// Bounded Context.
package computergateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/fx"

	"github.com/talgent-work/contracts/gen/go/computer/private/v1/computerprivatev1connect"
	"github.com/talgent-work/workbench/internal/pkg/computerconnection"
)

// Config is the Gateway's deployment configuration.
type Config struct {
	// ComputerAddress and ControlToken reach Computer's private contract.
	ComputerAddress string `json:"computer-address" yaml:"computer-address" toml:"computer-address"`
	ControlToken    string `json:"control-token" yaml:"control-token" toml:"control-token"`
	// Replica names this replica; Endpoint is where it takes forwarded calls.
	Replica  string `json:"replica" yaml:"replica" toml:"replica"`
	Endpoint string `json:"endpoint" yaml:"endpoint" toml:"endpoint"`
	// ReleaseVersion is the platform release this deployment runs, X.Y.Z, the
	// version Talgent Computer is published to npm at; empty for a deployment
	// that is no release.
	ReleaseVersion string `json:"release-version" yaml:"release-version" toml:"release-version"`
}

func (c Config) validate() error {
	for name, value := range map[string]string{"computer address": c.ComputerAddress, "control token": c.ControlToken, "replica": c.Replica, "endpoint": c.Endpoint} {
		if strings.TrimSpace(value) == "" {
			return errors.New("computer gateway: " + name + " is required")
		}
	}
	if c.ReleaseVersion != "" && !releaseVersion.MatchString(c.ReleaseVersion) {
		return errors.New("computer gateway: release version must be X.Y.Z or empty")
	}
	return nil
}

var Module = fx.Module("service.computergateway",
	fx.Provide(newGateway),
	fx.Invoke(register),
)

func newGateway(lc fx.Lifecycle, config Config, client *http.Client, rdb *goredis.Client, logger *slog.Logger) (*Gateway, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	token := "Bearer " + config.ControlToken
	computers := computerprivatev1connect.NewConnectionServiceClient(connect.NewClient(connecthttp.NewTransport(client, strings.TrimRight(config.ComputerAddress, "/"),
		connecthttp.WithReadMaxBytes(0),
	), func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if info, ok := connect.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Authorization", token)
			}
			return next(ctx, spec)
		}
	}))
	gateway := NewGateway(computers, computerconnection.NewWriter(rdb), computerconnection.NewReader(rdb), Replica{Name: config.Replica, Endpoint: config.Endpoint, ControlToken: config.ControlToken, TargetVersion: config.ReleaseVersion}, logger)
	lc.Append(fx.StopHook(gateway.Stop))
	return gateway, nil
}

func register(mux *http.ServeMux, gateway *Gateway, config Config) {
	mux.HandleFunc("GET /computer/v1/connect", gateway.ServeConnect)
	mux.Handle("GET /computer/v1/release", Release{Version: config.ReleaseVersion})
	// The relay is reached inside the cluster only; no public route leads here.
	mux.HandleFunc("/relay/{computer}/works/{work}", gateway.ServeRelay)
	mux.HandleFunc("/relay/{computer}/works/{work}/{rest...}", gateway.ServeRelay)
	mux.HandleFunc("/relay/{computer}/reconciliation", gateway.ServeMachineRelay)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}
