package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/samber/oops"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// NativeConnections onboards an allocation's environment once and hands the
// durable process binding to Dispatcher. Onboarding runs each step once:
// first delivery when the environment runs, preparation until it exits, then
// the Harness start and Connect. A reconnect only resolves the environment
// again. It
// never attaches to the native protocol or interprets Agent execution
// completion.
type NativeConnections struct {
	provisioning *Provisioning
	credentials  *SandboxCredentials
	http         *http.Client
	dispatcher   dispatcherprivatev1connect.ExecutionServiceClient
	token        string
	feedback     ExecutionFeedback
	checkouts    CheckoutOutcomes
}

// CheckoutOutcomes records the outcomes the preparation process reports for
// the planned repositories.
type CheckoutOutcomes interface {
	Handle(context.Context, command.RecordRepositoryCheckouts) error
}

func NewNativeConnections(provisioning *Provisioning, credentials *SandboxCredentials, client *http.Client, address, token string, feedback ExecutionFeedback, checkouts CheckoutOutcomes) (*NativeConnections, error) {
	if provisioning == nil || credentials == nil || client == nil || feedback == nil || checkouts == nil || strings.TrimSpace(address) == "" || strings.TrimSpace(token) == "" {
		return nil, errors.New("agent: provisioning, HTTP client, checkout outcomes and authenticated Dispatcher endpoint are required")
	}
	return &NativeConnections{provisioning: provisioning, credentials: credentials, http: client, dispatcher: dispatcherprivatev1connect.NewExecutionServiceClient(connect.NewClient(connecthttp.NewTransport(client, address, connecthttp.WithReadMaxBytes(0)))), token: token, feedback: feedback, checkouts: checkouts}, nil
}

// launchTimeout bounds starting the Harness and connecting it, which hold the
// environment lock.
const launchTimeout = 30 * time.Second

// resolve finds the allocation's running environment through its authority.
// Its authoritative absence is the permanent loss of the Work's execution
// environment, since the live allocation is the Work's only one: Agent learns
// it and the allocation retires. An environment not running yet is pending.
func (n *NativeConnections) resolve(ctx context.Context, record allocationRecord) (environment, environmentState, error) {
	authority, err := n.provisioning.authority(record)
	if err != nil {
		return environment{}, environmentPending, err
	}
	box, state, err := authority.resolve(ctx, record, n.http)
	if state == environmentLost {
		return environment{}, environmentLost, n.lose(ctx, record)
	}
	return box, state, err
}

// lose reports the loss and retires the allocation. Agent applies the loss to
// the Work's current process without observing again, so only the Work's live
// allocation, of which a Work has one, may report it: a stale record of a
// retired allocation conflicts instead of failing a later start.
func (n *NativeConnections) lose(ctx context.Context, record allocationRecord) error {
	current, err := n.current(ctx, record)
	if err != nil {
		return err
	}
	if err := n.feedback.Handle(ctx, command.ExecutionFeedback{WorkID: current.SessionID, Kind: command.EnvironmentLost}); err != nil {
		return err
	}
	return n.update(ctx, current, "deleted_at = ?", n.provisioning.now().UnixMilli())
}

// deliver performs the first delivery once the environment runs, after which
// preparation follows. It reports whether preparation may start.
func (n *NativeConnections) deliver(ctx context.Context, record allocationRecord) (bool, error) {
	var prepared bool
	err := n.credentials.withLock(ctx, record.SandboxID, func() error {
		current, err := n.current(ctx, record)
		if err != nil || current.SandboxReady {
			prepared = err == nil
			return err
		}
		box, state, err := n.resolve(ctx, current)
		if err != nil || state != environmentRunning {
			// A pending environment is delivered when its authority reports it running.
			return err
		}
		if err := n.onboard(ctx, current, box); err != nil {
			return err
		}
		prepared = true
		return nil
	})
	return prepared, err
}

// redeliver replaces the credentials of a delivered environment with those
// Marketplace supplies now, after a credential change or a passed expiry. A
// credential Marketplace no longer supplies loses its binding; the Work keeps
// running. An environment not yet delivered receives current credentials at
// its first delivery.
func (n *NativeConnections) redeliver(ctx context.Context, record allocationRecord) error {
	return n.credentials.withLock(ctx, record.SandboxID, func() error {
		current, err := n.current(ctx, record)
		if err != nil || !current.SandboxReady {
			return err
		}
		authority, err := n.provisioning.authority(current)
		if err != nil {
			return err
		}
		delivery, delivers := authority.(credentialDelivery)
		if !delivers {
			return nil
		}
		delivered, err := delivery.deliver(ctx, current, true)
		if err != nil {
			return err
		}
		if err := n.recordServiceAccess(ctx, &current, delivered.Statuses); err != nil {
			return err
		}
		if millis := expiryMillis(delivered.ExpiresAt); millis != current.CredentialsExpireAt || delivered.ModelDigest != current.ModelCredentialDigest {
			return n.update(ctx, current, "credentials_expire_at = ?, model_credential_digest = ?", millis, delivered.ModelDigest)
		}
		return nil
	})
}

// onboard removes surplus environments, delivers the Work's credentials when
// its authority delivers any, and records the environment ready.
func (n *NativeConnections) onboard(ctx context.Context, current allocationRecord, box environment) error {
	authority, err := n.provisioning.authority(current)
	if err != nil {
		return err
	}
	if surplus, ok := authority.(surplusEnvironments); ok {
		if err := surplus.prune(ctx, current, box); err != nil {
			return err
		}
	}
	delivery, delivers := authority.(credentialDelivery)
	if !delivers {
		return n.update(ctx, current, "sandbox_ready = 1, preparation_recovering = 0")
	}
	delivered, err := delivery.deliver(ctx, current, false)
	if err != nil {
		clear, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		return errors.Join(err, delivery.withdraw(clear, current))
	}
	if err := n.recordServiceAccess(ctx, &current, delivered.Statuses); err != nil {
		return err
	}
	return n.update(ctx, current, "sandbox_ready = 1, preparation_recovering = 0, credentials_expire_at = ?, model_credential_digest = ?", expiryMillis(delivered.ExpiresAt), delivered.ModelDigest)
}

func expiryMillis(expiresAt time.Time) int64 {
	if expiresAt.IsZero() {
		return 0
	}
	return expiresAt.UnixMilli()
}

func (n *NativeConnections) recordServiceAccess(ctx context.Context, record *allocationRecord, statuses []ServiceAccessStatus) error {
	encoded, err := json.Marshal(statuses)
	if err != nil {
		return err
	}
	var prior []ServiceAccessStatus
	if len(record.ServiceAccessJSON) > 0 {
		if err := json.Unmarshal(record.ServiceAccessJSON, &prior); err != nil {
			return err
		}
	}
	previous, err := json.Marshal(prior)
	if err != nil {
		return err
	}
	if bytes.Equal(encoded, previous) {
		return nil
	}
	if err := n.update(ctx, *record, "service_access_json = ?", encoded); err != nil {
		return err
	}
	record.ServiceAccessJSON = encoded
	record.Version++
	return nil
}

// drain withdraws the delivered credentials of a Work whose stop is admitted and
// discards an unfinished preparation, while its process may still need the
// connection to report its exit.
func (n *NativeConnections) drain(ctx context.Context, record allocationRecord) error {
	return n.credentials.withLock(ctx, record.SandboxID, func() error {
		current, err := n.current(ctx, record)
		if err != nil {
			return err
		}
		authority, err := n.provisioning.authority(current)
		if err != nil {
			return err
		}
		if delivery, delivers := authority.(credentialDelivery); delivers {
			if err := delivery.withdraw(ctx, current); err != nil {
				// An environment that is gone holds no credentials to clear;
				// only its authoritative absence, not a failed endpoint
				// lookup, says so.
				if _, state, lostErr := n.resolve(ctx, current); state == environmentLost {
					return lostErr
				}
				return err
			}
		}
		if current.PreparationProcessID == "" || current.EnvironmentPrepared {
			return nil
		}
		box, state, err := n.resolve(ctx, current)
		if err != nil || state != environmentRunning {
			return err
		}
		return n.discardPluginPreparation(ctx, &current, box.client)
	})
}

// prepare sees the preparation process through to its exit, bounded by ctx,
// then starts the Harness and connects it. Re-attaching after an interrupted
// attempt replays the preparation from its start.
func (n *NativeConnections) prepare(ctx context.Context, record allocationRecord) error {
	box, state, err := n.resolve(ctx, record)
	if err != nil || state == environmentLost {
		return err
	}
	if state == environmentPending {
		return errors.New("agent: a delivered environment is not running")
	}
	admitted, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return oops.With("operation", "decode_native_request").Wrap(err)
	}
	if !record.EnvironmentPrepared {
		if record.PreparationProcessID == "" {
			err := n.credentials.withLock(ctx, record.SandboxID, func() error {
				current, err := n.current(ctx, record)
				if err == nil {
					err = n.startPreparation(ctx, &current, box.client)
				}
				record = current
				return err
			})
			if err != nil {
				return err
			}
		}
		if err := n.prepareEnvironment(ctx, &record, box.client, admitted.Capabilities); err != nil {
			return err
		}
	}
	// The launch holds the environment lock, so it gets its own bound rather than
	// the remaining Startup Deadline.
	launch, release := context.WithTimeout(ctx, launchTimeout)
	defer release()
	ctx = launch
	return n.credentials.withLock(ctx, record.SandboxID, func() error {
		current, err := n.current(ctx, record)
		if err != nil {
			return err
		}
		// A stop admitted while preparation ran must not start the Harness.
		if err := n.credentials.works.current(ctx, current); err != nil {
			return err
		}
		if current.LaunchCommand == "" || current.NativeSessionID == "" {
			return domain.ErrExecutionRequest
		}
		if current.ProcessID == "" {
			id, err := box.client.Create(ctx, "/workspace", current.LaunchCommand)
			if err != nil {
				return err
			}
			// Persist only one winning handle before any caller can attach. An
			// uncertain write leaves a reserved handle for sandbox cleanup; it
			// must not delete a possibly committed winning handle.
			if err := n.update(ctx, current, "process_id = ?", id); err != nil {
				return err
			}
			current.ProcessID = id
		}
		return n.connect(ctx, current, box)
	})
}

// reconnect hands a running process to Dispatcher again after its connection
// ended. It resolves the environment only: no preparation, delivery or cleanup.
func (n *NativeConnections) reconnect(ctx context.Context, record allocationRecord) error {
	current, err := n.current(ctx, record)
	if err != nil {
		return err
	}
	if current.ProcessID == "" || current.ProcessID != record.ProcessID || current.ProcessExited {
		return errEnvironmentConflict
	}
	record = current
	box, state, err := n.resolve(ctx, record)
	if err != nil || state == environmentLost {
		return err
	}
	if state == environmentPending {
		return errors.New("agent: the environment of a running process is not running")
	}
	return n.connect(ctx, record, box)
}

func (n *NativeConnections) connect(ctx context.Context, record allocationRecord, box environment) error {
	request := &dispatcherprivatev1.ConnectRequest{
		WorkId:   record.SessionID,
		Endpoint: box.endpoint, AccessToken: box.token, ProcessId: record.ProcessID, NativeSessionId: record.NativeSessionID,
	}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+n.token)
	if _, err := n.dispatcher.Connect(ctx, request); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return oops.With("operation", "connect_native_execution").Wrap(err)
	}
	return nil
}

// current re-reads a live allocation that still has the recorded environment.
func (n *NativeConnections) current(ctx context.Context, record allocationRecord) (allocationRecord, error) {
	current, found, err := n.provisioning.get(ctx, record.ID)
	if err != nil {
		return current, err
	}
	if !found || current.DeletedAt != 0 || current.SandboxID != record.SandboxID {
		return current, errEnvironmentConflict
	}
	return current, nil
}

func (n *NativeConnections) update(ctx context.Context, record allocationRecord, assignment string, values ...any) error {
	p := n.provisioning
	args := append(values, p.now().UnixMilli(), record.ID, record.Version)
	result, err := p.db.Context(ctx).Exec(append([]any{"UPDATE execution_allocation SET " + assignment + ", updated_at = ?, version = version + 1 WHERE id = ? AND version = ? AND sandbox_id <> '' AND deleted_at = 0"}, args...)...)
	if err != nil {
		return oops.With("operation", "persist_native_connection").Wrap(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return oops.Wrap(err)
	}
	if changed != 1 {
		return errEnvironmentConflict
	}
	if p.ProgressCommitted != nil {
		p.ProgressCommitted(ctx, record.SessionID)
	}
	return nil
}
