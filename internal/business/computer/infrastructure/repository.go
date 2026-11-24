package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"time"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

var computerColumns = []string{"id", "kind", "owner_id", "name", "capacity", "occupied", "admission_policy", "waiting_line", "reachable", "unreachable_since", "talgent_computer_version", "claude_code_version", "claude_code_sandbox", "credential_digest", "credential_expires_at", "pending_credential_digest", "pending_credential_expires_at", "pending_credential_issued_at", "retired_credential_digest", "retired_credential_expires_at", "state", "version"}

// ComputerRepository persists Computers; it joins the Application
// transaction the context is in.
type ComputerRepository struct {
	db  *jimuxorm.DB
	now func() time.Time
}

var _ domain.ComputerRepository = (*ComputerRepository)(nil)

func NewComputerRepository(db *jimuxorm.DB, now func() time.Time) *ComputerRepository {
	return &ComputerRepository{db: db, now: now}
}

func (r *ComputerRepository) Get(ctx context.Context, id string) (*domain.Computer, error) {
	row := new(computerDO)
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	found, err := session.Cols(computerColumns...).Where("id = ? AND deleted_at = 0", id).Get(row)
	if err != nil {
		return nil, oops.With("operation", "computer.get").With("computer_id", id).Wrap(err)
	}
	if !found {
		return nil, oops.With("operation", "computer.get").With("computer_id", id).Wrap(domain.ErrComputerNotFound)
	}
	computer, err := computerFromDO(row)
	if err != nil {
		return nil, oops.With("operation", "computer.rehydrate").With("computer_id", id).Wrap(err)
	}
	return computer, nil
}

func (r *ComputerRepository) FindByCredential(ctx context.Context, digest string) (*domain.Computer, error) {
	row := new(computerDO)
	if digest == "" {
		return nil, oops.With("operation", "computer.find_by_credential").Wrap(domain.ErrComputerNotFound)
	}
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	found, err := session.Cols(computerColumns...).
		Where("(credential_digest = ? OR pending_credential_digest = ? OR retired_credential_digest = ?) AND kind = ? AND deleted_at = 0", digest, digest, digest, string(domain.KindManaged)).Get(row)
	if err != nil {
		return nil, oops.With("operation", "computer.find_by_credential").Wrap(err)
	}
	if !found {
		return nil, oops.With("operation", "computer.find_by_credential").Wrap(domain.ErrComputerNotFound)
	}
	computer, err := computerFromDO(row)
	if err != nil {
		return nil, oops.With("operation", "computer.rehydrate").With("computer_id", row.ID).Wrap(err)
	}
	return computer, nil
}

func (r *ComputerRepository) Save(ctx context.Context, computer *domain.Computer) error {
	if computer == nil {
		return oops.With("operation", "computer.save").Wrap(errors.New("nil aggregate"))
	}
	row, err := computerToDO(computer)
	if err != nil {
		return oops.With("operation", "computer.encode").With("computer_id", computer.ID).Wrap(err)
	}
	session, err := r.db.Session(ctx)
	if err != nil {
		return err
	}
	now := r.now().UnixMilli()
	if computer.Version == 0 {
		row.Version, row.CreatedAt, row.UpdatedAt = 1, now, now
		affected, err := session.Insert(row)
		if err != nil {
			if isDuplicate(err) {
				return oops.With("operation", "computer.insert").With("computer_id", computer.ID).Wrap(domain.ErrConcurrentModification)
			}
			return oops.With("operation", "computer.insert").With("computer_id", computer.ID).Wrap(err)
		}
		if affected != 1 {
			return oops.With("operation", "computer.insert").With("computer_id", computer.ID).Wrap(sql.ErrNoRows)
		}
		return nil
	}
	row.UpdatedAt = now
	affected, err := session.Cols("name", "capacity", "occupied", "admission_policy", "waiting_line", "reachable", "unreachable_since", "talgent_computer_version", "claude_code_version", "claude_code_sandbox", "credential_digest", "credential_expires_at", "pending_credential_digest", "pending_credential_expires_at", "pending_credential_issued_at", "retired_credential_digest", "retired_credential_expires_at", "state", "updated_at").
		Incr("version").Where("id = ? AND version = ? AND deleted_at = 0", computer.ID, computer.Version).Update(row)
	if err != nil {
		return oops.With("operation", "computer.update").With("computer_id", computer.ID).Wrap(err)
	}
	if affected != 1 {
		return oops.With("operation", "computer.update").With("computer_id", computer.ID).With("version", computer.Version).Wrap(domain.ErrConcurrentModification)
	}
	return nil
}

var deskColumns = []string{"id", "work_id", "computer_id", "demand", "state", "since", "opened_at", "version"}

// DeskRepository persists Desks; it joins the Application transaction the
// context is in.
type DeskRepository struct {
	db  *jimuxorm.DB
	now func() time.Time
}

var _ domain.DeskRepository = (*DeskRepository)(nil)

func NewDeskRepository(db *jimuxorm.DB, now func() time.Time) *DeskRepository {
	return &DeskRepository{db: db, now: now}
}

func (r *DeskRepository) Get(ctx context.Context, id string) (*domain.Desk, error) {
	row := new(deskDO)
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	found, err := session.Cols(deskColumns...).Where("id = ? AND deleted_at = 0", id).Get(row)
	if err != nil {
		return nil, oops.With("operation", "desk.get").With("desk_id", id).Wrap(err)
	}
	if !found {
		return nil, oops.With("operation", "desk.get").With("desk_id", id).Wrap(domain.ErrDeskNotFound)
	}
	desk, err := deskFromDO(row)
	if err != nil {
		return nil, oops.With("operation", "desk.rehydrate").With("desk_id", id).Wrap(err)
	}
	return desk, nil
}

func (r *DeskRepository) Save(ctx context.Context, desk *domain.Desk) error {
	if desk == nil {
		return oops.With("operation", "desk.save").Wrap(errors.New("nil aggregate"))
	}
	row, err := deskToDO(desk)
	if err != nil {
		return oops.With("operation", "desk.encode").With("desk_id", desk.ID).Wrap(err)
	}
	session, err := r.db.Session(ctx)
	if err != nil {
		return err
	}
	now := r.now().UnixMilli()
	if desk.Version == 0 {
		row.Version, row.CreatedAt, row.UpdatedAt = 1, now, now
		affected, err := session.Insert(row)
		if err != nil {
			if isDuplicate(err) {
				return oops.With("operation", "desk.insert").With("desk_id", desk.ID).Wrap(domain.ErrDeskTaken)
			}
			return oops.With("operation", "desk.insert").With("desk_id", desk.ID).Wrap(err)
		}
		if affected != 1 {
			return oops.With("operation", "desk.insert").With("desk_id", desk.ID).Wrap(sql.ErrNoRows)
		}
		return nil
	}
	row.UpdatedAt = now
	affected, err := session.Cols("state", "since", "updated_at").Incr("version").
		Where("id = ? AND version = ? AND deleted_at = 0", desk.ID, desk.Version).Update(row)
	if err != nil {
		return oops.With("operation", "desk.update").With("desk_id", desk.ID).Wrap(err)
	}
	if affected != 1 {
		return oops.With("operation", "desk.update").With("desk_id", desk.ID).With("version", desk.Version).Wrap(domain.ErrConcurrentModification)
	}
	return nil
}

func (r *DeskRepository) ListOpen(ctx context.Context, computerID string) ([]*domain.Desk, error) {
	var rows []deskDO
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	err = session.Cols(deskColumns...).
		Where("computer_id = ? AND state IN (?, ?) AND deleted_at = 0", computerID, string(domain.DeskStateWaiting), string(domain.DeskStateOccupied)).
		OrderBy("id").Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "desk.list_open").With("computer_id", computerID).Wrap(err)
	}
	desks := make([]*domain.Desk, 0, len(rows))
	for i := range rows {
		desk, err := deskFromDO(&rows[i])
		if err != nil {
			return nil, oops.With("operation", "desk.rehydrate").With("desk_id", rows[i].ID).Wrap(err)
		}
		desks = append(desks, desk)
	}
	return desks, nil
}

const mysqlDuplicateEntry = 1062

func isDuplicate(err error) bool {
	var duplicate *drivermysql.MySQLError
	return errors.As(err, &duplicate) && duplicate.Number == mysqlDuplicateEntry
}
