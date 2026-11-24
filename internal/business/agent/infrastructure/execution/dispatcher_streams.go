package execution

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

// Dispatcher publishes the stream it reads for a Work as a Redis hash per
// Work in the shared Redis; Agent reads these two fields and nothing else.
const (
	dispatcherOwnerKeyPrefix = "dispatcher:owner:"
	dispatcherProcessField   = "process_id"
	dispatcherEndpointField  = "endpoint"
)

// DispatcherStreams reads the stream ownership Dispatcher publishes, so
// Agent's sweep sees a running process nobody reads without asking
// Dispatcher. A record outlives a replica's death until a peer takes the
// stream over, so its presence means some replica reads or will resume the
// stream; its absence means none will.
type DispatcherStreams struct {
	rdb *goredis.Client
}

var _ StreamOwners = DispatcherStreams{}

func NewDispatcherStreams(rdb *goredis.Client) DispatcherStreams {
	return DispatcherStreams{rdb: rdb}
}

func (d DispatcherStreams) Reading(ctx context.Context, workID string) (StreamReading, bool, error) {
	values, err := d.rdb.HMGet(ctx, dispatcherOwnerKeyPrefix+workID, dispatcherProcessField, dispatcherEndpointField).Result()
	if err != nil {
		return StreamReading{}, false, oops.With("operation", "dispatcher_stream.read").With("work_id", workID).Wrap(err)
	}
	process, _ := values[0].(string)
	endpoint, _ := values[1].(string)
	if process == "" {
		return StreamReading{}, false, nil
	}
	return StreamReading{ProcessID: process, Endpoint: endpoint}, true, nil
}
