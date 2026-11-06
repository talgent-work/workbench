package infrastructure

import (
	"context"
	"time"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/computer/application"
	"github.com/talgent-work/workbench/internal/business/computer/domain"
	pkgmysql "github.com/talgent-work/workbench/pkg/mysql"
)

// Queries reads Computers and Desks for listing.
type Queries struct {
	executors *pkgmysql.Executors
}

var _ application.Queries = (*Queries)(nil)

func NewQueries(executors *pkgmysql.Executors) *Queries {
	return &Queries{executors: executors}
}

func (q *Queries) ListUsableComputers(ctx context.Context, personID string) ([]application.UsableComputer, error) {
	var rows []computerDO
	err := q.executors.Resolve(ctx).Cols("id", "kind", "name", "reachable", "claude_code_sandbox").
		Where("deleted_at = 0 AND state = ? AND (kind = ? OR (kind = ? AND owner_id = ?))", string(domain.StateActive), string(domain.KindCloud), string(domain.KindManaged), personID).
		OrderBy("kind = 'managed', CASE WHEN kind = 'managed' THEN -created_at ELSE created_at END, id").Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "computer.list_usable").Wrap(err)
	}
	computers := make([]application.UsableComputer, 0, len(rows))
	for _, row := range rows {
		computers = append(computers, application.UsableComputer{ID: row.ID, Kind: domain.Kind(row.Kind), Name: row.Name, Reachable: row.Reachable, Sandbox: row.ClaudeCodeSandbox})
	}
	return computers, nil
}

func (q *Queries) ListOpenDesks(ctx context.Context, openedBefore time.Time, afterDeskID string, limit int) ([]application.OpenDesk, error) {
	var rows []deskDO
	err := q.executors.Resolve(ctx).Cols("id", "work_id").
		Where("deleted_at = 0 AND state IN (?, ?) AND opened_at < ? AND id > ?", string(domain.DeskStateWaiting), string(domain.DeskStateOccupied), openedBefore.UnixMilli(), afterDeskID).
		OrderBy("id").Limit(limit).Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "desk.list_open").Wrap(err)
	}
	desks := make([]application.OpenDesk, 0, len(rows))
	for _, row := range rows {
		desks = append(desks, application.OpenDesk{DeskID: row.ID, WorkID: row.WorkID})
	}
	return desks, nil
}

var managedColumns = []string{"id", "name", "reachable", "unreachable_since", "talgent_computer_version", "claude_code_version", "created_at", "capacity", "occupied", "admission_policy", "waiting_line"}

func managedFromDO(row computerDO) (application.ManagedComputer, error) {
	room, err := roomFromDO(&row)
	if err != nil {
		return application.ManagedComputer{}, oops.With("computer_id", row.ID).Wrap(err)
	}
	return application.ManagedComputer{
		ID: row.ID, Name: row.Name, RegisteredAt: fromMillis(row.CreatedAt), NeverConnected: row.TalgentComputerVersion == "",
		Presence: domain.Presence{Reachable: row.Reachable, UnreachableSince: fromMillis(row.UnreachableSince)},
		Versions: domain.ReportedVersions{TalgentComputer: row.TalgentComputerVersion, ClaudeCode: row.ClaudeCodeVersion},
		Desks:    room.capacity.Desks, Occupied: room.occupied.Desks, Waiting: len(room.waitingLine), Policy: domain.AdmissionPolicy(row.AdmissionPolicy),
	}, nil
}

func (q *Queries) ListManagedComputers(ctx context.Context, ownerID string) ([]application.ManagedComputer, error) {
	var rows []computerDO
	err := q.executors.Resolve(ctx).Cols(managedColumns...).
		Where("deleted_at = 0 AND state = ? AND kind = ? AND owner_id = ?", string(domain.StateActive), string(domain.KindManaged), ownerID).
		OrderBy("created_at DESC, id DESC").Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "computer.list_managed").Wrap(err)
	}
	computers := make([]application.ManagedComputer, 0, len(rows))
	for _, row := range rows {
		computer, err := managedFromDO(row)
		if err != nil {
			return nil, err
		}
		computers = append(computers, computer)
	}
	return computers, nil
}

func (q *Queries) GetManagedComputer(ctx context.Context, ownerID, computerID string) (application.ManagedComputer, error) {
	var row computerDO
	found, err := q.executors.Resolve(ctx).Cols(managedColumns...).
		Where("id = ? AND deleted_at = 0 AND state = ? AND kind = ? AND owner_id = ?", computerID, string(domain.StateActive), string(domain.KindManaged), ownerID).Get(&row)
	if err != nil {
		return application.ManagedComputer{}, oops.With("operation", "computer.get_managed").Wrap(err)
	}
	if !found {
		return application.ManagedComputer{}, domain.ErrComputerNotFound
	}
	return managedFromDO(row)
}

func (q *Queries) ListManagedPresence(ctx context.Context, afterID string, limit int) ([]application.ManagedPresence, error) {
	var rows []computerDO
	err := q.executors.Resolve(ctx).Cols("id", "reachable").
		Where("deleted_at = 0 AND kind = ? AND state = ? AND id > ?", string(domain.KindManaged), string(domain.StateActive), afterID).
		OrderBy("id").Limit(limit).Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "computer.list_managed_presence").Wrap(err)
	}
	page := make([]application.ManagedPresence, 0, len(rows))
	for _, row := range rows {
		page = append(page, application.ManagedPresence{ID: row.ID, Reachable: row.Reachable})
	}
	return page, nil
}
