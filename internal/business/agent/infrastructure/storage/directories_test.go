package storage

import (
	"testing"

	"github.com/stretchr/testify/require"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A located Directory reaches Storage with its Backend and Lifetime; one
// whose Backend or Lifetime Storage's contract does not know is refused
// instead of being sent unspecified.
func TestALocatedDirectoryOfAnUnknownBackendOrLifetimeIsRefused(t *testing.T) {
	directory := domain.LocatedDirectory{Directory: domain.Directory{Name: "home", Owner: []domain.OwnerFact{{Condition: domain.ConditionWork, Value: "w"}}}, Backend: domain.BackendNAS, Lifetime: domain.LifetimeWork}
	located, err := locatedToProto(directory)
	require.NoError(t, err)
	require.Equal(t, storageprivatev1.Backend_BACKEND_NAS, located.GetBackend())
	require.Equal(t, storageprivatev1.Lifetime_LIFETIME_WORK, located.GetLifetime())

	unknownBackend := directory
	unknownBackend.Backend = "tape"
	_, err = locatedToProto(unknownBackend)
	require.Error(t, err)

	unknownLifetime := directory
	unknownLifetime.Lifetime = "forever"
	_, err = locatedToProto(unknownLifetime)
	require.Error(t, err)
}
