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
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// NativeConnections realizes the steps an execution takes inside its bound
// environment: credential delivery and withdrawal, preparation, the Harness
// start, and handing the launched process to Dispatcher. It writes only the
// technical columns of the execution's record; the execution records each
// step's outcome. It never attaches to the native protocol or interprets
// Agent execution completion.
type NativeConnections struct {
	provisioning *Provisioning
	credentials  *SandboxCredentials
	http         *http.Client
	dispatcher   dispatcherprivatev1connect.ExecutionServiceClient
	token        string
}

func NewNativeConnections(provisioning *Provisioning, credentials *SandboxCredentials, client *http.Client, address, token string) (*NativeConnections, error) {
	if provisioning == nil || credentials == nil || client == nil || strings.TrimSpace(address) == "" || strings.TrimSpace(token) == "" {
		return nil, errors.New("agent: provisioning, HTTP client and authenticated Dispatcher endpoint are required")
	}
	return &NativeConnections{provisioning: provisioning, credentials: credentials, http: client, dispatcher: dispatcherprivatev1connect.NewExecutionServiceClient(connect.NewClient(connecthttp.NewTransport(client, address, connecthttp.WithReadMaxBytes(0)))), token: token}, nil
}

// resolve finds the execution's bound environment running through its
// authority; an environment not running now is an error of the step, whose
// loss the execution's observation decides.
func (n *NativeConnections) resolve(ctx context.Context, record executionRecord) (environment, error) {
	authority, err := n.provisioning.authority(record)
	if err != nil {
		return environment{}, err
	}
	box, state, err := authority.resolve(ctx, record, n.http)
	if err != nil {
		return environment{}, err
	}
	if state != environmentRunning {
		return environment{}, errEnvironmentNotRunning
	}
	return box, nil
}

// errEnvironmentNotRunning fails a step whose bound environment does not run
// now; the execution decides it again from its next observation.
var errEnvironmentNotRunning = errors.New("agent: the bound execution environment is not running")

// deliver delivers the Work's current credentials into the bound
// environment, or replaces those delivered before, and reports what it
// delivered. A first delivery that fails clears whatever it wrote. An
// authority that delivers none uses the machine's own.
func (n *NativeConnections) deliver(ctx context.Context, record executionRecord, supply command.CredentialSupply, redelivery bool, holding command.EnvironmentHolding) (domain.CredentialDelivery, error) {
	authority, err := n.provisioning.authority(record)
	if err != nil {
		return domain.CredentialDelivery{}, err
	}
	delivery, delivers := authority.(credentialDelivery)
	if !delivers {
		return domain.CredentialDelivery{}, nil
	}
	var delivered deliveredCredentials
	err = n.credentials.withLock(ctx, record.SandboxID, func() error {
		current, err := n.provisioning.current(ctx, record)
		if err != nil {
			return err
		}
		delivered, err = delivery.deliver(ctx, current, supply, redelivery, holding)
		if err != nil {
			if redelivery {
				return err
			}
			clear, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			return errors.Join(err, delivery.withdraw(clear, current))
		}
		return n.recordServiceAccess(ctx, current, delivered.Statuses)
	})
	if err != nil {
		return domain.CredentialDelivery{}, err
	}
	return domain.CredentialDelivery{Digest: delivered.ModelDigest, ExpiresAt: delivered.ExpiresAt}, nil
}

func (n *NativeConnections) recordServiceAccess(ctx context.Context, record executionRecord, statuses []ServiceAccessStatus) error {
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
	return n.provisioning.technical(ctx, record, "service_access_json = ?", encoded)
}

// withdraw clears the delivered credentials of a run its Work asked to exit,
// and discards an unfinished preparation, while its process may still need
// the connection to report its exit. An environment that is gone holds no
// credentials to clear; only its authoritative absence, not a failed
// endpoint lookup, says so.
func (n *NativeConnections) withdraw(ctx context.Context, record executionRecord) error {
	authority, err := n.provisioning.authority(record)
	if err != nil {
		return err
	}
	return n.credentials.withLock(ctx, record.SandboxID, func() error {
		current, err := n.provisioning.current(ctx, record)
		if err != nil {
			return err
		}
		if delivery, delivers := authority.(credentialDelivery); delivers {
			if err := delivery.withdraw(ctx, current); err != nil {
				if _, state, _ := authority.resolve(ctx, current, n.http); state == environmentLost {
					return nil
				}
				return err
			}
		}
		if current.PreparationProcessID == "" || current.PreparationEnded {
			return nil
		}
		box, state, err := authority.resolve(ctx, current, n.http)
		if err != nil || state != environmentRunning {
			return err
		}
		return n.discardPluginPreparation(ctx, &current, box.client)
	})
}

// prepare sees the preparation process through to its exit, bounded by ctx,
// and reports each selected Plugin's installation and each planned
// repository's checkout. Re-attaching after an interrupted attempt replays
// the preparation from its start.
func (n *NativeConnections) prepare(ctx context.Context, record executionRecord, holding command.EnvironmentHolding) (domain.PreparationReport, error) {
	var none domain.PreparationReport
	box, err := n.resolve(ctx, record)
	if err != nil {
		return none, err
	}
	admitted, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return none, oops.With("operation", "decode_native_request").Wrap(err)
	}
	if record.PreparationProcessID == "" {
		err := n.credentials.withLock(ctx, record.SandboxID, func() error {
			if err := holding(ctx); err != nil {
				return err
			}
			current, err := n.provisioning.current(ctx, record)
			if err == nil && current.PreparationProcessID == "" {
				err = n.startPreparation(ctx, &current, box.client, admitted)
			}
			record = current
			return err
		})
		if err != nil {
			return none, err
		}
	}
	return n.prepareEnvironment(ctx, &record, box.client, admitted)
}

// launch starts the Harness in the bound environment and returns its
// process. The execution records it before it is connected; an uncertain
// record leaves the process to the environment's deletion.
func (n *NativeConnections) launch(ctx context.Context, record executionRecord, holding command.EnvironmentHolding) (string, error) {
	box, err := n.resolve(ctx, record)
	if err != nil {
		return "", err
	}
	var id string
	err = n.credentials.withLock(ctx, record.SandboxID, func() error {
		// An exit asked while preparation ran must not start the Harness
		// (Exit request ends delivery).
		if err := holding(ctx); err != nil {
			return err
		}
		current, err := n.provisioning.current(ctx, record)
		if err != nil {
			return err
		}
		if current.LaunchCommand == "" || current.NativeSessionID == "" {
			return domain.ErrExecutionRequest
		}
		id, err = box.client.Create(ctx, "/workspace", current.LaunchCommand)
		return err
	})
	return id, err
}

// connect hands the launched process to Dispatcher. It resolves the
// environment only: no preparation, delivery or cleanup.
func (n *NativeConnections) connect(ctx context.Context, record executionRecord) error {
	box, err := n.resolve(ctx, record)
	if err != nil {
		return err
	}
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
