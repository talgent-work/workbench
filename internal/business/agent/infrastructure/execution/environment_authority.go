package execution

import (
	"context"
	"errors"
	"net/http"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// An allocation's realization is fixed when it is registered and recorded
// with it (execution_allocation.realization): which environment authority
// holds its environment for its whole life.
const (
	// realizationCloud is a sandbox on the cluster's Cloud.
	realizationCloud = "cloud"
	// realizationManaged is the Work's directory on its Managed Computer,
	// reached through Computer Gateway's relay.
	realizationManaged = "managed"
)

// errManagedNotConfigured means this deployment has no relay to Managed
// Computers, so no step of a Managed allocation can be taken.
var errManagedNotConfigured = errors.New("agent: Managed Computers are not configured")

// environmentState is what resolving an allocation's environment found.
type environmentState int

const (
	environmentPending environmentState = iota
	environmentRunning
	environmentLost
)

// environment is a resolved running environment of an allocation.
type environment struct {
	endpoint string
	// token is the execd access token of the environment.
	token string
	// spec is a Cloud sandbox's specification, which its surplus pruning
	// matches; a Managed environment has none.
	spec   sdk.CreateSandboxRequest
	client *execd.Client
}

// environmentSpecification is what registration fixes for an allocation's
// environment: its document, kept in specification_json, and the Provider
// Authorizations its deliveries use, as a JSON array.
type environmentSpecification struct {
	document       []byte
	authorizations []byte
}

// environmentAuthority acts on one allocation's environment and reports what
// it found. The allocation state machine (Provisioning, NativeConnections,
// EnvironmentRecovery, ProcessObservations, Reclaimer) sequences every step
// and records every durable fact; it chooses the authority once per
// allocation from the recorded realization.
type environmentAuthority interface {
	// launchLayout is where the Harness and its preparation run.
	launchLayout() launchLayout
	// specify fixes the environment's specification from the allocation, its
	// execd access token and its Work credential.
	specify(ctx context.Context, request domain.StartExecutionRequest, mounts domain.SandboxMounts, owner Allocation, token, credential string) (environmentSpecification, error)
	// claim prepares the first submission and returns what is frozen with
	// the submission claim: the credential plan, or nil for none.
	claim(ctx context.Context, record allocationRecord) ([]byte, error)
	// create submits the environment once and returns its reference, empty
	// while unresolved; errCreationRefused when the authority refused it.
	create(ctx context.Context, record allocationRecord) (string, error)
	// find returns the reference of a claimed allocation's environment,
	// empty while unresolved.
	find(ctx context.Context, record allocationRecord) (string, error)
	// resolve finds the allocation's running environment, reaching it with
	// client unless the authority keeps its own. environmentLost is its
	// authoritative loss.
	resolve(ctx context.Context, record allocationRecord, client *http.Client) (environment, environmentState, error)
	// lost reports, without reaching the environment, whether it is
	// authoritatively gone.
	lost(ctx context.Context, record allocationRecord) (bool, error)
	// release reclaims the compute a removal obligation names and reports
	// whether it is gone.
	release(ctx context.Context, allocation allocationRecord, removal removalRecord) (bool, error)
	// workCredential is the Work credential issued to the environment.
	workCredential(record allocationRecord) (string, error)
}

// credentialDelivery is an authority that delivers the Work's credentials
// into its environment. One without it uses the machine's own.
type credentialDelivery interface {
	// deliver fetches the Work's current credentials into the environment;
	// a redelivery replaces those delivered before.
	deliver(ctx context.Context, record allocationRecord, redelivery bool) (deliveredCredentials, error)
	// withdraw clears the delivered credentials.
	withdraw(ctx context.Context, record allocationRecord) error
	// modelDigest identifies the model key a delivery carries now.
	modelDigest(record allocationRecord) (string, error)
}

// deliveredCredentials is what a delivery handed the environment: the service
// access observed, the earliest declared expiry of its material, zero when
// none expires, and the model key it carried.
type deliveredCredentials struct {
	Statuses    []ServiceAccessStatus
	ExpiresAt   time.Time
	ModelDigest string
}

// surplusEnvironments is an authority whose creation can leave more than one
// environment for an allocation. One without it never duplicates.
type surplusEnvironments interface {
	// prune removes a live allocation's environments other than env.
	prune(ctx context.Context, record allocationRecord, env environment) error
	// pruneRetired removes every late environment of a retired allocation.
	pruneRetired(ctx context.Context, record allocationRecord) error
}

// observedCompute is an authority that itself observes whether a retired
// allocation's compute is gone. For one without it, the completed removal
// obligation says so.
type observedCompute interface {
	released(ctx context.Context, record allocationRecord) (bool, error)
}

// fencingEnvironment is an authority whose machine fences its Work processes
// during a Disconnection, so a Work may end a run its allocation
// never saw exit.
type fencingEnvironment interface {
	// outlived reports that the allocation's Work no longer holds the run it
	// backs.
	outlived(ctx context.Context, record allocationRecord) (bool, error)
}

// unreachedReclamation is an authority whose Work reclaims the environment of
// a run it no longer reaches during a Disconnection, in place of the exit
// the allocation never saw.
type unreachedReclamation interface {
	// unreached reports that the allocation's Work asks for that release.
	unreached(ctx context.Context, record allocationRecord) (bool, error)
}

// authority is the environment authority of an allocation.
func (p *Provisioning) authority(record allocationRecord) (environmentAuthority, error) {
	return p.authorityOf(record.Realization)
}

// authorityOf is the one place a realization becomes its authority. A
// Managed allocation in a deployment without the relay fails here.
func (p *Provisioning) authorityOf(realization string) (environmentAuthority, error) {
	switch realization {
	case realizationCloud:
		return cloudEnvironment{sandboxes: p.sandboxes, credentials: p.credentials, works: p.credentials.heldWorks()}, nil
	case realizationManaged:
		if p.managed == nil {
			return nil, errManagedNotConfigured
		}
		return *p.managed, nil
	default:
		return nil, oops.With("realization", realization).Errorf("agent: unknown execution realization")
	}
}

// unreached reports a live allocation whose process never exited while its
// Work reclaims the run beyond reach, so its release is authorized as an
// exit's would be.
func (p *Provisioning) unreached(ctx context.Context, record allocationRecord) (bool, error) {
	if record.DeletedAt != 0 || record.ProcessExited {
		return false, nil
	}
	authority, err := p.authority(record)
	if err != nil {
		return false, err
	}
	reclamation, ok := authority.(unreachedReclamation)
	if !ok {
		return false, nil
	}
	return reclamation.unreached(ctx, record)
}

// outlived reports a live allocation on a fencing authority that never saw
// its run exit while its Work no longer holds that run: the Work ended it as
// the machine's fence did.
func (p *Provisioning) outlived(ctx context.Context, record allocationRecord) (bool, error) {
	if record.DeletedAt != 0 || record.ProcessExited {
		return false, nil
	}
	authority, err := p.authority(record)
	if err != nil {
		return false, err
	}
	fencing, ok := authority.(fencingEnvironment)
	if !ok {
		return false, nil
	}
	return fencing.outlived(ctx, record)
}
