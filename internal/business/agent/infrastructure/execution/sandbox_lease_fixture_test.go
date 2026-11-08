package execution

import (
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	shared "github.com/talgent-work/workbench/internal/pkg/opensandbox"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// fixtureLease is the deployment's default: two-hour leases renewed once less
// than an hour remains.
var fixtureLease = domain.LeaseTerms{Duration: 2 * time.Hour, RenewalThreshold: time.Hour}

// fixtureCreationTimeout and fixtureCreationWait are the server's creation
// timeout and the creation wait sandboxes are configured with.
var (
	fixtureCreationTimeout = 5 * time.Minute
	fixtureCreationWait    = domain.CreationWait(fixtureCreationTimeout + 30*time.Second)
	// fixtureCreationWaits are the creation waits reconciliation counts
	// creation deadlines with, the Cloud's matching the sandboxes'.
	fixtureCreationWaits = domain.CreationWaits{Cloud: fixtureCreationWait, Managed: domain.CreationWait(30 * time.Second)}
)

func (f *lifecycleFixture) now() time.Time {
	if f.clock == nil {
		return time.Now()
	}
	return f.clock()
}

// leased gives info the expiry the create's timeout asks for, as the server
// does, unless the fixture creates sandboxes without one.
func (f *lifecycleFixture) leased(info *sdk.SandboxInfo) {
	if f.create.Timeout == nil || f.unleased {
		return
	}
	expiresAt := f.now().Add(time.Duration(*f.create.Timeout) * time.Second).UTC()
	info.ExpiresAt = &expiresAt
}

// expire deletes every sandbox whose expiry passed, as the BatchSandbox
// controller enforces it.
func (f *lifecycleFixture) expire() {
	expired := func(info sdk.SandboxInfo) bool {
		return info.ExpiresAt != nil && !f.now().Before(*info.ExpiresAt)
	}
	if f.info != nil && expired(*f.info) {
		f.info = nil
	}
	maps.DeleteFunc(f.others, func(_ string, info sdk.SandboxInfo) bool { return expired(info) })
}

// renew moves a sandbox's expiry to the requested time, which may be any
// future time, a shorter one included; a sandbox created without an expiry
// is refused, as OpenSandbox refuses it.
func (f *lifecycleFixture) renew(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/"), "/renew-expiration")
	var info *sdk.SandboxInfo
	if f.info != nil && f.info.ID == id {
		info = f.info
	} else if other, found := f.others[id]; found {
		info = &other
		defer func() { f.others[id] = *info }()
	}
	if info == nil {
		http.Error(w, `{"code":"SANDBOX_NOT_FOUND"}`, 404)
		return
	}
	var request sdk.RenewExpirationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || info.ExpiresAt == nil || !request.ExpiresAt.After(f.now()) {
		http.Error(w, `{"code":"INVALID_EXPIRATION"}`, 400)
		return
	}
	f.renewals = append(f.renewals, request.ExpiresAt)
	expiresAt := request.ExpiresAt.UTC()
	info.ExpiresAt = &expiresAt
	_ = json.NewEncoder(w).Encode(sdk.RenewExpirationResponse{ExpiresAt: expiresAt})
}

// A lease needs a renewal threshold below its duration, or it would be
// renewed on every reconciliation.
func TestSandboxLeaseNeedsAThresholdBelowItsDuration(t *testing.T) {
	client, err := shared.NewLifecycleClient(http.DefaultClient, shared.Config{Address: "http://opensandbox.invalid/v1", APIKey: "lifecycle-secret"})
	require.NoError(t, err)
	for _, lease := range []domain.LeaseTerms{{}, {Duration: time.Hour, RenewalThreshold: time.Hour}, {Duration: time.Hour}, {Duration: time.Millisecond, RenewalThreshold: time.Microsecond}} {
		cfg := config()
		cfg.Lease = lease
		_, err := NewSandboxes(client, fixturePods(), cfg)
		require.Error(t, err, "lease %+v", lease)
	}
}

// fixtureSandboxNamespace is where the fixture's sandbox Pods run.
const fixtureSandboxNamespace = "talgent-sandbox"

// fixturePods reads the given Pods as the sandbox namespace holds them.
func fixturePods(pods ...runtime.Object) *SandboxPods {
	reader, err := NewSandboxPods(fake.NewClientset(pods...), fixtureSandboxNamespace)
	if err != nil {
		panic(err)
	}
	return reader
}
