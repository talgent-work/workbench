// Package streamownership is the published read contract for Dispatcher's
// stream ownership records: which Harness process, at which
// endpoint, Dispatcher reads for a Work. Dispatcher writes the record; other
// modules only read it here and never depend on Dispatcher's internals.
package streamownership

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

// The record is a Redis hash per Work. Only these fields are published.
const (
	ProcessField  = "process_id"
	EndpointField = "endpoint"
)

// Key names the Work's ownership record.
func Key(workID string) string { return "dispatcher:owner:" + workID }

// Reading is the process whose stream Dispatcher has recorded for a Work. A
// record outlives a replica's death until a peer takes the stream over, so its
// presence means some replica reads or will resume the stream; its absence
// means none will.
type Reading struct {
	ProcessID, Endpoint string
}

type Reader struct {
	rdb *goredis.Client
}

func NewReader(rdb *goredis.Client) *Reader { return &Reader{rdb: rdb} }

// Read returns the Work's recorded reading, if any.
func (r *Reader) Read(ctx context.Context, workID string) (Reading, bool, error) {
	values, err := r.rdb.HMGet(ctx, Key(workID), ProcessField, EndpointField).Result()
	if err != nil {
		return Reading{}, false, oops.Wrap(err)
	}
	process, _ := values[0].(string)
	endpoint, _ := values[1].(string)
	if process == "" {
		return Reading{}, false, nil
	}
	return Reading{ProcessID: process, Endpoint: endpoint}, true, nil
}
