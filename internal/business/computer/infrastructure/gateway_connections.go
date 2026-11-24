package infrastructure

import (
	"context"
	"errors"

	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/computer/application"
)

// gatewayConnectionKeyPrefix is the Computer Gateway's published connection
// record: a Computer holds a connection exactly while its key exists. Computer
// reads the key's existence and nothing else.
const gatewayConnectionKeyPrefix = "computer:connection:"

// GatewayConnections reads the connection records the Computer Gateway
// publishes in the shared Redis.
type GatewayConnections struct{ rdb goredis.UniversalClient }

var _ application.Connections = (*GatewayConnections)(nil)

func NewGatewayConnections(rdb *goredis.Client) *GatewayConnections {
	return &GatewayConnections{rdb: rdb}
}

// Connected tells which of the Computers hold a connection now.
func (g *GatewayConnections) Connected(ctx context.Context, computerIDs []string) (map[string]bool, error) {
	pipe := g.rdb.Pipeline()
	checks := make([]*goredis.IntCmd, len(computerIDs))
	for i, id := range computerIDs {
		checks[i] = pipe.Exists(ctx, gatewayConnectionKeyPrefix+id)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
		return nil, oops.With("operation", "computer_gateway_connections.read").Wrap(err)
	}
	connected := make(map[string]bool, len(computerIDs))
	for i, id := range computerIDs {
		connected[id] = checks[i].Val() == 1
	}
	return connected, nil
}
