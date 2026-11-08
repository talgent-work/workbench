package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// A Managed Work's credentials are delivered to Talgent Computer, which keeps
// them outside every Work root and gives them to no process: its local proxy
// adds them to the requests a run's processes send through it.
// The Work credential, with the Space Token of the Work's input source, goes
// only to the platform Connector and the native API on its origin, and a
// Model Configuration key only to its Model Template's endpoint. Provider
// credentials stay with the platform.

// workCredentials is the body of the relay's credentials PUT. It carries
// secrets: it is never logged, recorded or echoed.
type workCredentials struct {
	// Model is absent on the machine's own Claude login.
	Model   *workModelCredential  `json:"model,omitempty"`
	Gateway workGatewayCredential `json:"gateway"`
	// API is where the Work's native API calls go, with the Gateway's
	// identity; absent when Managed Works have no direct API.
	API *workAPICredential `json:"api,omitempty"`
}

type workAPICredential struct {
	URL string `json:"url"`
}

type workModelCredential struct {
	BaseURL string `json:"base_url"`
	Key     string `json:"key"`
}

type workGatewayCredential struct {
	URL            string `json:"url"`
	WorkID         string `json:"work_id"`
	WorkCredential string `json:"work_credential"`
	SpaceToken     string `json:"space_token,omitempty"`
}

// deliverCredentials replaces the Work's credentials on its machine.
func (m *ManagedComputers) deliverCredentials(ctx context.Context, spec managedSpecification, workID string, credentials workCredentials) error {
	body, err := json.Marshal(credentials)
	if err != nil {
		return oops.Wrap(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, m.endpoint(spec, workID)+"/credentials", bytes.NewReader(body))
	if err != nil {
		return oops.Wrap(err)
	}
	request.Header.Set("X-Talgent-Control-Token", m.token)
	request.Header.Set("Content-Type", "application/json")
	return m.do(ctx, request, "deliver_work_credentials")
}

// forgetCredentials makes the machine forget the Work's credentials.
func (m *ManagedComputers) forgetCredentials(ctx context.Context, spec managedSpecification, workID string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, m.endpoint(spec, workID)+"/credentials", nil)
	if err != nil {
		return oops.Wrap(err)
	}
	request.Header.Set("X-Talgent-Control-Token", m.token)
	return m.do(ctx, request, "forget_work_credentials")
}

// deliver hands the Work's current credentials to its machine's local proxy,
// replacing those delivered before. As on a Cloud, the Model Configuration's
// current key is delivered only while it still belongs to the endpoint the
// start was realized with; otherwise a redelivery keeps the key already
// delivered, and a first delivery fails, since without a model the proxy
// would leave the Harness on the machine's own Claude login. Nothing
// delivered declares an expiry. The Work's inputs, whose downloads
// authenticate with the Work credential delivered here, are synced as their
// own step once it is delivered.
func (m ManagedEnvironment) deliver(ctx context.Context, record allocationRecord, redelivery bool, holding command.EnvironmentHolding) (deliveredCredentials, error) {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return deliveredCredentials{}, err
	}
	admitted, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return deliveredCredentials{}, oops.With("operation", "decode_native_request").Wrap(err)
	}
	if err := holding(ctx); err != nil {
		return deliveredCredentials{}, err
	}
	credentials := workCredentials{Gateway: workGatewayCredential{URL: m.machines.gateway, WorkID: record.SessionID, WorkCredential: spec.WorkCredential, SpaceToken: admitted.InputSource.SpaceToken}}
	if spec.APIAddress != "" {
		credentials.API = &workAPICredential{URL: spec.APIAddress}
	}
	model := renewalCredential(m.models, admitted.WorkModel, spec.ModelEndpoint)
	keep := false
	if !admitted.WorkModel.MachineLogin {
		switch {
		case model.Key == "" && redelivery:
			keep = true
		case model.Key == "":
			return deliveredCredentials{}, errModelConfigurationNotProvided
		default:
			credentials.Model = &workModelCredential{BaseURL: model.Endpoint, Key: model.Key}
		}
	}
	if !keep {
		if err := m.machines.deliverCredentials(ctx, spec, record.SessionID, credentials); err != nil {
			return deliveredCredentials{}, err
		}
	}
	if keep {
		return deliveredCredentials{}, nil
	}
	// A stop admitted while delivering must not leave the credentials behind.
	if err := holding(ctx); err != nil {
		clear, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return deliveredCredentials{}, errors.Join(err, m.machines.forgetCredentials(clear, spec, record.SessionID))
	}
	return deliveredCredentials{ModelDigest: model.digest()}, nil
}

// withdraw makes the machine forget the Work's credentials. A machine not
// reachable now keeps the withdrawal due; one that holds nothing of the Work
// any more, its reconciliation having removed it, forgot them with it.
func (m ManagedEnvironment) withdraw(ctx context.Context, record allocationRecord) error {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return err
	}
	if err := m.machines.forgetCredentials(ctx, spec, record.SessionID); !errors.Is(err, errWorkUnknown) {
		return err
	}
	return nil
}

// modelDigest identifies the Model Configuration key a delivery carries now,
// empty on the machine's own Claude login.
func (m ManagedEnvironment) modelDigest(record allocationRecord) (string, error) {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return "", err
	}
	admitted, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return "", err
	}
	return renewalCredential(m.models, admitted.WorkModel, spec.ModelEndpoint).digest(), nil
}
