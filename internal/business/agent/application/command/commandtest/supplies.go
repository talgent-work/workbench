package commandtest

import (
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
)

// Supplies obtains an execution's environment supplies from storage, for
// Works that selected no repository and no service authorization.
func Supplies(storage command.StoragePort) *command.EnvironmentSupplies {
	return command.NewEnvironmentSupplies(authorizationcatalogtest.NoRepositories{}, authorizationcatalogtest.NoServices{}, storage)
}
