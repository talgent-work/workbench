//go:build integration

package agent_test

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/computer"
)

// testCloud is the Cloud of a test cluster whose quota is "executions" in
// "sandboxes"; each run takes one CPU and 1Gi.
func testCloud(cpu, memory, pods string) computer.Config {
	return computer.Config{ControlToken: "computer", VerificationURI: "https://workbench.test/computers/connect", Cloud: computer.CloudConfig{
		ID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Name: "Test Cloud",
		Capacity:  map[string]string{"requests.cpu": cpu, "requests.memory": memory, "pods": pods},
		Namespace: "sandboxes", QuotaName: "executions",
		Demand: map[string]string{"requests.cpu": "1", "requests.memory": "1Gi"},
	}}
}

// testRedis gives Computer the Redis its registrations and connection
// records live in.
func testRedis(t *testing.T) fx.Option {
	server := miniredis.RunT(t)
	return fx.Provide(func() *goredis.Client { return goredis.NewClient(&goredis.Options{Addr: server.Addr()}) })
}
