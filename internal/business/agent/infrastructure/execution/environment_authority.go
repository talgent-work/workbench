package execution

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// An execution's realization is fixed when it begins and recorded with it
// (agent_execution.realization): which environment authority holds its
// environment for its whole life.
const (
	// realizationCloud is a sandbox on the cluster's Cloud.
	realizationCloud = "cloud"
	// realizationManaged is the Work's directory on its Managed Computer,
	// reached through Computer Gateway's relay.
	realizationManaged = "managed"
)

// errManagedNotConfigured means this deployment has no relay to Managed
// Computers, so no step of a Managed execution can be taken.
var errManagedNotConfigured = errors.New("agent: Managed Computers are not configured")

// environmentState is what resolving an execution's environment found.
type environmentState int

const (
	environmentPending environmentState = iota
	environmentRunning
	environmentLost
)

// environment is a resolved running environment of an execution.
type environment struct {
	endpoint string
	// token is the execd access token of the environment.
	token  string
	client *execd.Client
}

// environmentSpecification is what specification fixes for an execution's
// environment: its document, kept in specification_json, and the Provider
// Authorizations its deliveries use, as a JSON array.
type environmentSpecification struct {
	document       []byte
	authorizations []byte
}

// environmentView is what observing an execution's environments found: the
// environments carrying its ownership, the bound one's endpoint while it
// runs, and whether the authority confirms every environment lost because
// the Work's Computer was removed.
type environmentView struct {
	environments []domain.ObservedEnvironment
	endpoint     string
	removed      bool
}

// environmentAuthority acts on one execution's environments and reports what
// it finds. The execution decides every step; the adapter
// (ExecutionEnvironment) chooses the authority once per execution from its
// recorded realization.
type environmentAuthority interface {
	// launchLayout is where the Harness and its preparation run.
	launchLayout() launchLayout
	// specify fixes the environment's specification from the execution's
	// LaunchSpecification and its execd access token.
	specify(ctx context.Context, request domain.LaunchSpecification, mounts domain.SandboxMounts, owner SandboxOwner, token string) (environmentSpecification, error)
	// claim returns what is frozen with the specification from what
	// Application supplied: the credential plan, or nil for none.
	claim(record executionRecord, supply command.CreationSupply) ([]byte, error)
	// create submits the environment once and returns its reference, empty
	// while unresolved; errCreationRefused when the authority refused it.
	create(ctx context.Context, record executionRecord) (string, error)
	// observe lists the environments carrying the execution's ownership; the
	// bound one is confirmed by its own identity.
	observe(ctx context.Context, record executionRecord, retired bool) (environmentView, error)
	// remove deletes one environment carrying the execution's ownership.
	remove(ctx context.Context, record executionRecord, id string) error
	// resolve finds the bound environment running, reaching it with client
	// unless the authority keeps its own. environmentLost is its
	// authoritative loss.
	resolve(ctx context.Context, record executionRecord, client *http.Client) (environment, environmentState, error)
}

// credentialDelivery is an authority that delivers the Work's credentials
// into its environment. One without it uses the machine's own.
type credentialDelivery interface {
	// deliver fetches the Work's current credentials into the environment;
	// a redelivery replaces those delivered before.
	// It asks holding before each write and clears what it wrote once
	// holding refuses.
	deliver(ctx context.Context, record executionRecord, supply command.CredentialSupply, redelivery bool, holding command.EnvironmentHolding) (deliveredCredentials, error)
	// withdraw clears the delivered credentials.
	withdraw(ctx context.Context, record executionRecord) error
	// modelDigest identifies the model key a delivery carries now.
	modelDigest(record executionRecord) (string, error)
}

// leasing is an authority whose environments expire unless renewed. One
// without it leases nothing.
type leasing interface {
	// renew moves the bound environment's expiry to expiresAt and returns
	// the expiry the authority answered.
	renew(ctx context.Context, record executionRecord, expiresAt time.Time) (time.Time, error)
}

// deliveredCredentials is what a delivery handed the environment: the service
// access observed, the earliest declared expiry of its material, zero when
// none expires, and the model key it carried.
type deliveredCredentials struct {
	Statuses    []command.ServiceAccessStatus
	ExpiresAt   time.Time
	ModelDigest string
}

// authority is the environment authority of an execution.
func (p *Provisioning) authority(record executionRecord) (environmentAuthority, error) {
	return p.authorityOf(record.Realization)
}

// authorityOf is the one place a realization becomes its authority. A
// Managed execution in a deployment without the relay fails here.
func (p *Provisioning) authorityOf(realization string) (environmentAuthority, error) {
	switch realization {
	case realizationCloud:
		return cloudEnvironment{sandboxes: p.sandboxes, credentials: p.credentials}, nil
	case realizationManaged:
		if p.managed == nil {
			return nil, errManagedNotConfigured
		}
		return *p.managed, nil
	default:
		return nil, oops.With("realization", realization).Errorf("agent: unknown execution realization")
	}
}
