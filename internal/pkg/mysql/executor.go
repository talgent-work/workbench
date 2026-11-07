package mysql

import (
	"context"
	"errors"

	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/pkg/transaction"
)

// Executors resolves the statement executor of one database for a
// Repository: the session of the Application transaction the context is in,
// or the engine itself outside one. It is also that database's Transactor.
type Executors struct {
	engine *xorm.Engine
}

var _ transaction.Transactor = (*Executors)(nil)

func NewExecutors(engine *xorm.Engine) *Executors {
	return &Executors{engine: engine}
}

// scope marks a context inside one Executors' transaction; the owner keeps a
// session of another database from being joined.
type scope struct {
	owner   *Executors
	session *xorm.Session
}

type scopeKey struct{}

func (e *Executors) current(ctx context.Context) *xorm.Session {
	if s, ok := ctx.Value(scopeKey{}).(scope); ok && s.owner == e {
		return s.session
	}
	return nil
}

// Resolve returns the executor for one statement.
func (e *Executors) Resolve(ctx context.Context) xorm.Interface {
	if session := e.current(ctx); session != nil {
		return session
	}
	return e.engine.Context(ctx)
}

// Within begins one transaction, runs fn once and commits on success.
func (e *Executors) Within(ctx context.Context, fn func(context.Context) error) error {
	if e.current(ctx) != nil {
		return errors.New("mysql: a transaction is already active in this context")
	}
	session := e.engine.NewSession().Context(ctx)
	defer session.Close()
	if err := session.Begin(); err != nil {
		return err
	}
	if err := fn(context.WithValue(ctx, scopeKey{}, scope{owner: e, session: session})); err != nil {
		return errors.Join(err, session.Rollback())
	}
	return session.Commit()
}
