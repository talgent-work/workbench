//go:build integration

package computer_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"

	computerprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1/computerprivatev1connect"
	computerpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/computer/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/computer/public/v1/computerpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/computer"
	"github.com/talgent-work/workbench/pkg/authn"
	"github.com/talgent-work/workbench/pkg/mysqltest"
)

type clients struct {
	registration computerpublicv1connect.RegistrationServiceClient
	computers    computerpublicv1connect.ComputerServiceClient
	connections  computerprivatev1connect.ConnectionServiceClient
	admission    computerprivatev1connect.AdmissionServiceClient
	// strangers calls the connection contract with a control token Computer
	// does not accept.
	strangers computerprivatev1connect.ConnectionServiceClient
}

func start(t *testing.T) clients {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	kube := fake.NewClientset(&corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard}})
	redis := miniredis.RunT(t)
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	config := computer.Config{ControlToken: "control", VerificationURI: "https://workbench.test/computers/connect", Cloud: computer.CloudConfig{
		ID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Name: "Test Cloud", Namespace: "sandboxes", QuotaName: "executions",
		Capacity: map[string]string{"requests.cpu": "8", "requests.memory": "8Gi", "pods": "8"},
		Demand:   map[string]string{"requests.cpu": "1", "requests.memory": "1Gi"},
	}}
	app := fx.New(computer.Module, fx.NopLogger,
		fx.Supply(mux, slog.Default(), config),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"workbench"`))),
		fx.Provide(func() kubernetes.Interface { return kube }),
		fx.Provide(func() *goredis.Client { return goredis.NewClient(&goredis.Options{Addr: redis.Addr()}) }),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, app.Start(ctx))
	t.Cleanup(func() { _ = app.Stop(context.Background()) })
	control := connect.WithInterceptors(header("Authorization", "Bearer control"))
	return clients{
		registration: computerpublicv1connect.NewRegistrationServiceClient(server.Client(), server.URL),
		computers:    computerpublicv1connect.NewComputerServiceClient(server.Client(), server.URL),
		connections:  computerprivatev1connect.NewConnectionServiceClient(server.Client(), server.URL, control),
		admission:    computerprivatev1connect.NewAdmissionServiceClient(server.Client(), server.URL, control),
		strangers:    computerprivatev1connect.NewConnectionServiceClient(server.Client(), server.URL, connect.WithInterceptors(header("Authorization", "Bearer wrong"))),
	}
}

func header(name, value string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set(name, value)
			return next(ctx, req)
		}
	}
}

// as signs a request in as a person, as the gateway does.
func as[T any](account string, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set(authn.HeaderAuthenticatedBy, "envoy")
	req.Header().Set(authn.HeaderSubject, account)
	return req
}

// Account ids are UUIDs.
const (
	person = "0195f3c1-0000-7000-8000-000000000001"
	other  = "0195f3c1-0000-7000-8000-000000000002"
)

func TestAPersonRegistersAMachineAndSeesItOnline(t *testing.T) {
	c := start(t)
	ctx := context.Background()
	started, err := c.registration.StartRegistration(ctx, connect.NewRequest(&computerpublicv1.StartRegistrationRequest{MachineName: "build-box"}))
	require.NoError(t, err)
	require.Contains(t, started.Msg.GetVerificationUriComplete(), "https://workbench.test/computers/connect?code=")
	pending, err := c.registration.RedeemRegistration(ctx, connect.NewRequest(&computerpublicv1.RedeemRegistrationRequest{DeviceCode: started.Msg.GetDeviceCode()}))
	require.NoError(t, err)
	require.Equal(t, computerpublicv1.RedemptionStatus_REDEMPTION_STATUS_PENDING, pending.Msg.GetStatus())

	shown, err := c.computers.GetRegistration(ctx, as(person, &computerpublicv1.GetRegistrationRequest{UserCode: started.Msg.GetUserCode()}))
	require.NoError(t, err)
	require.Equal(t, "build-box", shown.Msg.GetMachineName())
	_, err = c.computers.ApproveRegistration(ctx, as(person, &computerpublicv1.ApproveRegistrationRequest{UserCode: started.Msg.GetUserCode()}))
	require.NoError(t, err)
	_, err = c.computers.ApproveRegistration(ctx, as(other, &computerpublicv1.ApproveRegistrationRequest{UserCode: started.Msg.GetUserCode()}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	redeemed, err := c.registration.RedeemRegistration(ctx, connect.NewRequest(&computerpublicv1.RedeemRegistrationRequest{DeviceCode: started.Msg.GetDeviceCode()}))
	require.NoError(t, err)
	require.Equal(t, computerpublicv1.RedemptionStatus_REDEMPTION_STATUS_REGISTERED, redeemed.Msg.GetStatus())
	_, err = c.registration.RedeemRegistration(ctx, connect.NewRequest(&computerpublicv1.RedeemRegistrationRequest{DeviceCode: started.Msg.GetDeviceCode()}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "the credential is issued once")

	authenticated, err := c.connections.Authenticate(ctx, connect.NewRequest(&computerprivatev1.AuthenticateRequest{Credential: redeemed.Msg.GetCredential()}))
	require.NoError(t, err)
	require.Equal(t, redeemed.Msg.GetComputerId(), authenticated.Msg.GetComputerId())
	forged, err := c.connections.Authenticate(ctx, connect.NewRequest(&computerprivatev1.AuthenticateRequest{Credential: "tc_forged"}))
	require.NoError(t, err)
	require.Equal(t, computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_REJECTED, forged.Msg.GetRefusal())

	// Agent learns what each Computer is, to reach it through its realization.
	described, err := c.admission.DescribeComputer(ctx, connect.NewRequest(&computerprivatev1.DescribeComputerRequest{ComputerId: redeemed.Msg.GetComputerId()}))
	require.NoError(t, err)
	require.Equal(t, computerprivatev1.ComputerKind_COMPUTER_KIND_MANAGED, described.Msg.GetKind())
	require.Equal(t, person, described.Msg.GetOwnerId())
	require.False(t, described.Msg.GetRemoved())
	require.Equal(t, "build-box", described.Msg.GetName(), "its telemetry names it")
	cloud, err := c.admission.DescribeComputer(ctx, connect.NewRequest(&computerprivatev1.DescribeComputerRequest{ComputerId: "01a19bc3-075a-7c57-b42b-7fdf73b611ed"}))
	require.NoError(t, err)
	require.Equal(t, computerprivatev1.ComputerKind_COMPUTER_KIND_CLOUD, cloud.Msg.GetKind())
	require.Equal(t, "Test Cloud", cloud.Msg.GetName())
	_, err = c.admission.DescribeComputer(ctx, connect.NewRequest(&computerprivatev1.DescribeComputerRequest{ComputerId: "0195f3c1-0000-7000-8000-0000000000ff"}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	listed, err := c.computers.ListComputers(ctx, as(person, &computerpublicv1.ListComputersRequest{}))
	require.NoError(t, err)
	require.Len(t, listed.Msg.GetComputers(), 1, "the Cloud never appears")
	require.True(t, listed.Msg.GetComputers()[0].GetPresence().GetNeverConnected())

	// The Gateway reports reach; the Computer shows online, renamed, and
	// only to its owner.
	_, err = c.connections.ReportReachable(ctx, connect.NewRequest(&computerprivatev1.ReportReachableRequest{ComputerId: redeemed.Msg.GetComputerId(), Versions: &computerprivatev1.ReportedVersions{TalgentComputer: "0.1.0"}}))
	require.NoError(t, err)
	renamed, err := c.computers.RenameComputer(ctx, as(person, &computerpublicv1.RenameComputerRequest{ComputerId: redeemed.Msg.GetComputerId(), Name: "Studio"}))
	require.NoError(t, err)
	require.Equal(t, "Studio", renamed.Msg.GetComputer().GetName())
	require.True(t, renamed.Msg.GetComputer().GetPresence().GetReachable())
	require.Equal(t, "0.1.0", renamed.Msg.GetComputer().GetTalgentComputerVersion())
	// Start Work learns whether the machine can provide Claude Code's sandbox.
	usable, err := c.computers.ListUsableComputers(ctx, as(person, &computerpublicv1.ListUsableComputersRequest{}))
	require.NoError(t, err)
	require.False(t, usable.Msg.GetComputers()[len(usable.Msg.GetComputers())-1].GetClaudeCodeSandbox())
	_, err = c.connections.ReportReachable(ctx, connect.NewRequest(&computerprivatev1.ReportReachableRequest{ComputerId: redeemed.Msg.GetComputerId(), Versions: &computerprivatev1.ReportedVersions{TalgentComputer: "0.1.0"}, ClaudeCodeSandbox: true}))
	require.NoError(t, err)
	usable, err = c.computers.ListUsableComputers(ctx, as(person, &computerpublicv1.ListUsableComputersRequest{}))
	require.NoError(t, err)
	machine := usable.Msg.GetComputers()[len(usable.Msg.GetComputers())-1]
	require.Equal(t, redeemed.Msg.GetComputerId(), machine.GetId())
	require.True(t, machine.GetClaudeCodeSandbox())
	require.False(t, usable.Msg.GetComputers()[0].GetClaudeCodeSandbox(), "a Cloud provides none")
	others, err := c.computers.ListComputers(ctx, as(other, &computerpublicv1.ListComputersRequest{}))
	require.NoError(t, err)
	require.Empty(t, others.Msg.GetComputers())
	_, err = c.computers.RenameComputer(ctx, as(other, &computerpublicv1.RenameComputerRequest{ComputerId: redeemed.Msg.GetComputerId(), Name: "Mine"}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = c.connections.ReportUnreachable(ctx, connect.NewRequest(&computerprivatev1.ReportUnreachableRequest{ComputerId: redeemed.Msg.GetComputerId(), SinceUnixMs: time.Now().UnixMilli()}))
	require.NoError(t, err)
	listed, err = c.computers.ListComputers(ctx, as(person, &computerpublicv1.ListComputersRequest{}))
	require.NoError(t, err)
	presence := listed.Msg.GetComputers()[0].GetPresence()
	require.False(t, presence.GetReachable())
	require.NotZero(t, presence.GetUnreachableSinceUnixMs())
}
