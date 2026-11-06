// Package transaction is the provider-neutral contract for one Application
// use case that saves several Aggregate Roots of one bounded context in one
// local transaction.
package transaction

import "context"

// Transactor runs fn in one transaction: it commits when fn returns nil and
// rolls back otherwise. Repositories called with the derived context join it.
// The context must be used sequentially and not kept beyond fn.
type Transactor interface {
	Within(ctx context.Context, fn func(context.Context) error) error
}
