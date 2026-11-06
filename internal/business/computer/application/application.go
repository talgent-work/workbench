// Package application holds Computer's use cases: Clouds, admission of runs
// and the Desks they hold.
package application

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
	"github.com/talgent-work/workbench/internal/pkg/transaction"
)

// ClusterQuota supplies, for a Cloud, whether its cluster's quota fits one
// more run now. It is a preflight fact, not a reservation.
type ClusterQuota interface {
	FitsRun(ctx context.Context) (bool, error)
}

// Queries reads Computers and Desks for listing.
type Queries interface {
	ListUsableComputers(ctx context.Context, personID string) ([]UsableComputer, error)
	ListOpenDesks(ctx context.Context, openedBefore time.Time, afterDeskID string, limit int) ([]OpenDesk, error)
	// ListManagedComputers lists a person's Managed Computers that are not
	// removed, newest first.
	ListManagedComputers(ctx context.Context, ownerID string) ([]ManagedComputer, error)
	// GetManagedComputer reads one of a person's Managed Computers, or fails
	// with domain.ErrComputerNotFound.
	GetManagedComputer(ctx context.Context, ownerID, computerID string) (ManagedComputer, error)
	// ListManagedPresence pages through active Managed Computers in identity
	// order, after a Computer identity, with whether each is reachable.
	ListManagedPresence(ctx context.Context, afterID string, limit int) ([]ManagedPresence, error)
}

// Application is Computer's use-case entry.
type Application struct {
	computers     domain.ComputerRepository
	desks         domain.DeskRepository
	transactions  transaction.Transactor
	quota         ClusterQuota
	queries       Queries
	registrations Registrations
	connections   Connections
	cloud         Cloud
	minimum       string
	seatings      Seatings
	presences     Presences
	removals      Removals
	now           func() time.Time
	logger        *slog.Logger
}

// Dependencies groups what Computer's use cases consume.
type Dependencies struct {
	Computers    domain.ComputerRepository
	Desks        domain.DeskRepository
	Transactions transaction.Transactor
	Quota        ClusterQuota
	Queries      Queries
	// Registrations holds registrations until they are approved and redeemed.
	Registrations Registrations
	// Connections tells which Managed Computers the Computer Gateway holds.
	Connections Connections
	// Cloud is this cluster's Cloud as the platform sets it.
	Cloud Cloud
	// MinimumVersion is the oldest Talgent Computer release a Managed
	// Computer may take a run on; empty admits every release.
	MinimumVersion string
	// Seatings publishes Desk Assigned; nil publishes nothing.
	Seatings Seatings
	// Presences publishes Presence changes; nil publishes nothing.
	Presences Presences
	// Removals publishes Computer Removed; nil publishes nothing.
	Removals Removals
	Now      func() time.Time
	Logger   *slog.Logger
}

func NewApplication(deps Dependencies) *Application {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	seatings := deps.Seatings
	if seatings == nil {
		seatings = noSeatings{}
	}
	presences := deps.Presences
	if presences == nil {
		presences = noPresences{}
	}
	removals := deps.Removals
	if removals == nil {
		removals = noRemovals{}
	}
	return &Application{seatings: seatings, presences: presences, removals: removals, logger: logger, computers: deps.Computers, desks: deps.Desks, transactions: deps.Transactions, quota: deps.Quota, queries: deps.Queries, registrations: deps.Registrations, connections: deps.Connections, cloud: deps.Cloud, minimum: deps.MinimumVersion, now: now}
}

// attempts bounds retries of a change that lost a concurrent update of the
// same Computer.
const attempts = 8

func retryConcurrent(ctx context.Context, fn func() error) error {
	var err error
	for range attempts {
		if err = fn(); !errors.Is(err, domain.ErrConcurrentModification) {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return err
}

// MinimumVersion is the oldest Talgent Computer release a Managed Computer
// may take a run on, which its machine is told on each connection; empty
// admits every release.
func (a *Application) MinimumVersion() string { return a.minimum }
