package infrastructure

import (
	"context"
	"time"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/google/uuid"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/sealing"
)

// PersonalVariableSetRepository keeps each person's PersonalVariableSet in
// agent_personal_variable_set, its values sealed under the Agent key and
// bound to the person (Personal Variable sealing).
type PersonalVariableSetRepository struct {
	db     *jimuxorm.DB
	sealer *sealing.Sealer
	now    func() time.Time
}

var _ domain.PersonalVariableSetRepository = (*PersonalVariableSetRepository)(nil)

func NewPersonalVariableSetRepository(db *jimuxorm.DB, sealer *sealing.Sealer, now func() time.Time) *PersonalVariableSetRepository {
	return &PersonalVariableSetRepository{db: db, sealer: sealer, now: now}
}

func (r *PersonalVariableSetRepository) Get(ctx context.Context, personID string) (*domain.PersonalVariableSet, error) {
	var row personalVariableSetDO
	session, err := r.db.Session(ctx)
	if err != nil {
		return nil, err
	}
	found, err := session.Cols("id", "person_id", "variables", "version").Where("person_id = ? AND deleted_at = 0", personID).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "personal_variable_set.get").Wrap(err)
	}
	if !found {
		return nil, domain.ErrPersonalVariableSetNotFound
	}
	variables, err := r.sealer.Open(row.Variables, sealing.Person(row.PersonID))
	if err != nil {
		return nil, oops.With("operation", "personal_variable_set.open").Wrap(err)
	}
	if row.Version < 1 {
		return nil, oops.With("operation", "personal_variable_set.restore").Wrap(domain.ErrInvalidPersonalVariable)
	}
	set := &domain.PersonalVariableSet{PersonID: row.PersonID, Variables: variables, Version: row.Version}
	if err := set.Validate(); err != nil {
		return nil, oops.With("operation", "personal_variable_set.restore").Wrap(err)
	}
	return set, nil
}

func (r *PersonalVariableSetRepository) Save(ctx context.Context, set *domain.PersonalVariableSet) error {
	if set == nil {
		return domain.ErrInvalidPersonalVariable
	}
	if err := set.Validate(); err != nil {
		return err
	}
	sealed, err := r.sealer.Seal(set.Variables, sealing.Person(set.PersonID))
	if err != nil {
		return oops.With("operation", "personal_variable_set.seal").Wrap(err)
	}
	row := personalVariableSetDO{PersonID: set.PersonID, Variables: sealed, Version: set.Version + 1, UpdatedAt: r.now().UTC().UnixMilli()}
	session, err := r.db.Session(ctx)
	if err != nil {
		return err
	}
	if set.Version == 0 {
		row.ID, row.CreatedAt = uuid.Must(uuid.NewV7()).String(), row.UpdatedAt
		// A person's first set races another first set through the unique
		// person.
		if _, err = session.Insert(&row); duplicateKey(err) {
			return domain.ErrConcurrentModification
		}
	} else {
		var affected int64
		affected, err = session.Where("person_id = ? AND version = ? AND deleted_at = 0", set.PersonID, set.Version).
			Cols("variables", "version", "updated_at").Update(&row)
		if err == nil && affected != 1 {
			return domain.ErrConcurrentModification
		}
	}
	if err != nil {
		return oops.With("operation", "personal_variable_set.save").Wrap(err)
	}
	return nil
}
