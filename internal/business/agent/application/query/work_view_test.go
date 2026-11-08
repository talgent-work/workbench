package query_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestOwnerWorkActionsFollowStateAndPendingOperations(t *testing.T) {
	for _, test := range []struct {
		name, state     string
		pending         bool
		rejectedStartup bool
		deskWaiting     bool
		want            []string
	}{
		{name: "running", state: "working", want: []string{"stop"}},
		{name: "starting", state: "starting"},
		{name: "waiting for room", state: "starting", deskWaiting: true, want: []string{"stop", "archive"}},
		{name: "stopped", state: "stopped", want: []string{"resume", "archive"}},
		{name: "failed after successful startup", state: "failed", want: []string{"archive", "restart"}},
		{name: "failed startup", state: "failed", rejectedStartup: true, want: []string{"archive", "restart"}},
		{name: "closing", state: "closing"},
		{name: "closed", state: "closed"},
		{name: "archive pending", state: "stopped", pending: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ports := &historyPorts{owner: "actor", value: query.WorkView{WorkID: "session", State: test.state, ArchivePending: test.pending, DeskWaiting: test.deskWaiting}}
			if test.rejectedStartup {
				ports.value.StartupProgress = &query.StartupProgress{Status: "failed"}
				ports.value.StartupRequest = &query.WorkStartupRequest{InitialMessage: "Original task"}
			}
			value, err := query.NewGetWorkViewHandler(ports, ports).Handle(context.Background(), query.GetWorkView{ActorUserID: "actor", WorkID: "session"})
			require.NoError(t, err)
			require.Equal(t, "session", value.WorkID)
			require.Equal(t, test.want, value.AllowedActions)
		})
	}
}

func TestOnlyTheOwnerReadsItsWork(t *testing.T) {
	ctx := context.Background()
	request := query.GetWorkView{ActorUserID: "reader", WorkID: "session"}
	ports := &historyPorts{owner: "actor"}
	value, err := query.NewGetWorkViewHandler(ports, ports).Handle(ctx, request)
	require.ErrorIs(t, err, access.ErrAccessDenied)
	require.Zero(t, ports.reads, "ownership must precede Work data access")
	require.Empty(t, value.WorkID)
	ports.dataErr = errors.New("query unavailable")
	_, err = query.NewGetWorkViewHandler(ports, ports).Handle(ctx, query.GetWorkView{ActorUserID: "actor", WorkID: "session"})
	require.ErrorIs(t, err, ports.dataErr)
	_, err = query.NewGetWorkViewHandler(nil, nil).Handle(ctx, query.GetWorkView{WorkID: " "})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
}

type historyPorts struct {
	query.WorkQueryRepository
	owner   string
	dataErr error
	value   query.WorkView
	reads   int
}

func (p *historyPorts) AuthorizeWork(_ context.Context, subject, _ string) error {
	if subject == "" || subject != p.owner {
		return access.ErrAccessDenied
	}
	return nil
}
func (p *historyPorts) GetWorkView(context.Context, string) (query.WorkView, error) {
	p.reads++
	return p.value, p.dataErr
}

// During a Disconnection a Work is offered only what is admitted at once: a
// Working one its stop, never a close its input could refuse; a Stopping one
// its stop and its archive, on a Managed Computer and on a Cloud alike.
func TestADisconnectedWorkIsOfferedWhatIsAdmittedAtOnce(t *testing.T) {
	lost := time.Now()
	for state, want := range map[string][]string{"working": {"stop"}, "stopping": {"stop", "archive"}} {
		ports := &historyPorts{owner: "co-owner", value: query.WorkView{WorkID: "session", State: state, StartedBy: "actor", PermissionMode: domain.PermissionBypass, DisconnectedSince: lost}}
		value, err := query.NewGetWorkViewHandler(ports, ports).Handle(context.Background(), query.GetWorkView{ActorUserID: "co-owner", WorkID: "session"})
		require.NoError(t, err)
		require.Equal(t, want, value.AllowedActions, state)
	}
	ports := &historyPorts{owner: "actor", value: query.WorkView{WorkID: "session", State: "stopping", DisconnectedSince: lost}}
	value, err := query.NewGetWorkViewHandler(ports, ports).Handle(context.Background(), query.GetWorkView{ActorUserID: "actor", WorkID: "session"})
	require.NoError(t, err)
	require.Equal(t, []string{"stop", "archive"}, value.AllowedActions, "a Cloud Work's stop or close reclaims its sandbox at once")
	ports.value.DisconnectedSince = time.Time{}
	value, err = query.NewGetWorkViewHandler(ports, ports).Handle(context.Background(), query.GetWorkView{ActorUserID: "actor", WorkID: "session"})
	require.NoError(t, err)
	require.Empty(t, value.AllowedActions, "a connected Stopping Work waits for its exit")
}

// Only the person who started a running Managed Work is offered switching
// its permission mode; another Owner of its definition is not.
func TestOnlyTheStarterIsOfferedThePermissionModeSwitch(t *testing.T) {
	for actor, want := range map[string][]string{"actor": {"stop", "set_permission_mode"}, "co-owner": {"stop"}} {
		ports := &historyPorts{owner: actor, value: query.WorkView{WorkID: "session", State: "working", StartedBy: "actor", PermissionMode: domain.PermissionBypass}}
		value, err := query.NewGetWorkViewHandler(ports, ports).Handle(context.Background(), query.GetWorkView{ActorUserID: actor, WorkID: "session"})
		require.NoError(t, err)
		require.Equal(t, want, value.AllowedActions, actor)
	}
}
