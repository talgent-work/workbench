//go:build integration

package infrastructure_test

import (
	"bytes"
	"testing"
	"time"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/sealing"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/sealing/sealingtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// A PersonalVariableSet round-trips sealed under the Agent key; a stale or
// racing write is a concurrent modification, and a set opens only for its
// own person under the key that sealed it.
func TestPersonalVariableSetsKeepTheirValuesSealedPerPerson(t *testing.T) {
	ctx := t.Context()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	jdb, err := jimuxorm.New(db)
	require.NoError(t, err)
	sets := infrastructure.NewPersonalVariableSetRepository(jdb, sealingtest.Sealer(), time.Now)

	first, err := sets.Get(ctx, "person-a")
	require.NoError(t, err)
	require.Equal(t, &domain.PersonalVariableSet{PersonID: "person-a"}, first, "a person who keeps none reads the empty set")
	_, err = first.SetVariable("person-a", domain.PersonalVariable{Name: "TOKEN", Value: "one\ntwo"})
	require.NoError(t, err)
	racing, err := domain.NewPersonalVariableSet("person-a")
	require.NoError(t, err)
	require.NoError(t, sets.Save(ctx, first))
	require.ErrorIs(t, sets.Save(ctx, racing), domain.ErrConcurrentModification, "a second first set loses the race")

	loaded, err := sets.Get(ctx, "person-a")
	require.NoError(t, err)
	require.Equal(t, []domain.PersonalVariable{{Name: "TOKEN", Value: "one\ntwo"}}, loaded.Variables)
	stale, err := sets.Get(ctx, "person-a")
	require.NoError(t, err)
	_, err = loaded.RemoveVariable("person-a", "TOKEN")
	require.NoError(t, err)
	require.NoError(t, sets.Save(ctx, loaded))
	_, err = stale.SetVariable("person-a", domain.PersonalVariable{Name: "OTHER", Value: "v"})
	require.NoError(t, err)
	require.ErrorIs(t, sets.Save(ctx, stale), domain.ErrConcurrentModification)

	// Another person's row holding person-a's sealed value does not open.
	second, err := domain.NewPersonalVariableSet("person-b")
	require.NoError(t, err)
	require.NoError(t, sets.Save(ctx, second))
	_, err = db.Exec("UPDATE agent_personal_variable_set b JOIN agent_personal_variable_set a ON a.person_id = 'person-a' SET b.variables = a.variables WHERE b.person_id = 'person-b'")
	require.NoError(t, err)
	_, err = sets.Get(ctx, "person-b")
	require.Error(t, err, "a sealed value is bound to its person")
	other, err := sealing.NewSealer(bytes.Repeat([]byte{1}, sealing.KeySize))
	require.NoError(t, err)
	_, err = infrastructure.NewPersonalVariableSetRepository(jdb, other, time.Now).Get(ctx, "person-a")
	require.Error(t, err, "another key opens nothing")

	// A stored set that breaks its rules is corrupt data, never an invalid
	// request of whoever reads it.
	corrupt, err := sealingtest.Sealer().Seal([]domain.PersonalVariable{{Name: "1BAD", Value: "v"}}, sealing.Person("person-a"))
	require.NoError(t, err)
	_, err = db.Exec("UPDATE agent_personal_variable_set SET variables = ? WHERE person_id = 'person-a'", corrupt)
	require.NoError(t, err)
	_, err = sets.Get(ctx, "person-a")
	require.Error(t, err)
	require.NotErrorIs(t, err, domain.ErrInvalidPersonalVariable)
}
