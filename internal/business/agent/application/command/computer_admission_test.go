package command

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type usable []UsableComputer

func (u usable) ListUsableComputers(context.Context, string) ([]UsableComputer, error) { return u, nil }
func (usable) AdmitRun(context.Context, RunAdmission) (ComputerAnswer, error) {
	return ComputerAnswer{}, nil
}
func (usable) LeaveDesk(context.Context, string) error                              { return nil }
func (usable) ReleaseUnusedDesk(context.Context, string)                            {}
func (usable) ListOpenDesks(context.Context, time.Time, string) ([]OpenDesk, error) { return nil, nil }
func (usable) DeskSeated(context.Context, string) (time.Time, bool, error) {
	return time.Time{}, true, nil
}

func TestAWorkWithNoChosenComputerStartsOnTheFirstCloud(t *testing.T) {
	computer, err := workComputer(context.Background(), usable{{ID: "managed"}, {ID: "cloud-a", Cloud: true}, {ID: "cloud-b", Cloud: true}}, "person", "")
	require.NoError(t, err)
	require.Equal(t, UsableComputer{ID: "cloud-a", Cloud: true}, computer)
	_, err = workComputer(context.Background(), usable{{ID: "managed"}}, "person", "")
	require.ErrorIs(t, err, ErrComputerUnavailable)
}

func TestAChosenComputerIsTakenFromThoseUsableToItsStarter(t *testing.T) {
	computers := usable{{ID: "cloud-a", Cloud: true}, {ID: "managed"}}
	computer, err := workComputer(context.Background(), computers, "person", " managed ")
	require.NoError(t, err)
	require.Equal(t, UsableComputer{ID: "managed"}, computer)
	_, err = workComputer(context.Background(), computers, "person", "someone-elses")
	require.ErrorIs(t, err, domain.ErrComputerNotUsable)
}

func (usable) DescribeComputer(context.Context, string) (ComputerDescription, error) {
	return ComputerDescription{}, nil
}
