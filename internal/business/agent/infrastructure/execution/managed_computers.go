package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// ManagedComputers reaches the Managed realization's machines: the Work's
// Talgent Computer through Computer Gateway's relay, and Computer for whether
// the machine was removed. Where a Cloud allocation has a sandbox, a Managed
// allocation has the Work's one directory on the machine, opened by the relay's
// environment PUT and kept for the Work's life; the allocation itself, and its
// compute, is the run's processes only. The Work's credentials reach the
// machine's local proxy, never its processes, through the relay's credentials
// PUT; the proxy carries the Work credential to the platform Connector at its
// public address. The Work's inputs reach the machine as a manifest of Access
// Links it downloads from the same origin (managed_inputs.go), and the Work's
// files are read back through the relay (managed_files.go). A run's Harness
// sends its telemetry to the platform's public telemetry endpoint
// (managed_telemetry.go).
type ManagedComputers struct {
	http    *http.Client
	relay   *url.URL
	gateway string
	// content is where the machine downloads an Access Link.
	content string
	// api is where the machine's local proxy sends its Works' native API
	// calls; empty when Managed Works have none.
	api string
	// telemetry is where a run's Harness sends its OTLP telemetry.
	telemetry string
	token     string
	computers command.ComputerPort
	// fileIdle bounds the wait for each piece of a Work file read.
	fileIdle time.Duration
}

// ManagedAddresses are the platform's public addresses a Managed Computer
// reaches: Gateway, the platform Connector's HTTPS address, for its local
// proxy; Content, where the same origin serves Access Links, for its Works'
// inputs; API, where the same origin serves the native API, for its Works'
// direct calls, or empty for none; and Telemetry, the platform's HTTPS OTLP
// endpoint, for its Harness's telemetry.
type ManagedAddresses struct {
	Gateway, Content, API, Telemetry string
}

// NewManagedComputers reaches the machines through the relay and gives them
// the platform's public addresses.
func NewManagedComputers(client *http.Client, relayAddress string, addresses ManagedAddresses, controlToken string, computers command.ComputerPort) (*ManagedComputers, error) {
	relay, err := url.Parse(relayAddress)
	if client == nil || computers == nil || err != nil || relay.Host == "" || (relay.Scheme != "http" && relay.Scheme != "https") || relay.User != nil || relay.RawQuery != "" || relay.Fragment != "" || strings.TrimSpace(controlToken) == "" {
		return nil, errors.New("agent: HTTP client, Computer lookup, HTTP(S) relay address and control token are required for Managed Computers")
	}
	gateway, err := url.Parse(addresses.Gateway)
	// The machine reaches it over the internet with the Work credential.
	if err != nil || gateway.Scheme != "https" || gateway.Host == "" || gateway.User != nil || gateway.RawQuery != "" || gateway.Fragment != "" {
		return nil, errors.New("agent: the platform Connector's public HTTPS address is required for Managed Computers")
	}
	// Talgent Computer sends the Work's identity only to that origin, for its
	// inputs and its direct calls alike.
	content, ok := onOrigin(gateway, addresses.Content)
	if !ok {
		return nil, errors.New("agent: the public content address, on the platform Connector's origin, is required for Managed Computers")
	}
	api := ""
	if addresses.API != "" {
		if api, ok = onOrigin(gateway, addresses.API); !ok {
			return nil, errors.New("agent: the native API's public address must lie on the platform Connector's origin")
		}
	}
	telemetry, err := url.Parse(addresses.Telemetry)
	// The machine sends it its telemetry token over the internet.
	if err != nil || telemetry.Scheme != "https" || telemetry.Host == "" || telemetry.User != nil || telemetry.RawQuery != "" || telemetry.Fragment != "" {
		return nil, errors.New("agent: the platform's public HTTPS telemetry address is required for Managed Computers")
	}
	h := *client
	// Redirects would carry the control or execd token to another endpoint.
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ManagedComputers{http: &h, relay: relay, gateway: strings.TrimRight(gateway.String(), "/"), content: content, api: api, telemetry: strings.TrimRight(telemetry.String(), "/"), token: controlToken, computers: computers, fileIdle: workFileIdle}, nil
}

// onOrigin is address without a trailing slash when it names a path on
// origin's scheme and host, with no credentials, query or fragment.
func onOrigin(origin *url.URL, address string) (string, bool) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != origin.Scheme || parsed.Host != origin.Host || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") == "" {
		return "", false
	}
	return strings.TrimRight(parsed.String(), "/"), true
}

// managedSpecification is the recorded environment of a Managed allocation,
// held in specification_json in place of a Cloud's sandbox specification.
type managedSpecification struct {
	ComputerID string `json:"managedComputerId"`
	// AccessToken is the execd token every call of the Work presents.
	AccessToken string `json:"accessToken"`
	// WorkCredential is issued as on a Cloud and verified by WorkCredentials.
	// It reaches only the machine's local proxy, in the Work's credentials,
	// never Environment.
	WorkCredential string `json:"workCredential"`
	// ModelEndpoint is the Model Template's endpoint the start was realized
	// with, where the local proxy sends the Work's model requests; empty on
	// the machine's own Claude login. A Work with one opens its environment
	// with model_proxy.
	ModelEndpoint string `json:"modelEndpoint,omitempty"`
	// APIAddress is the native API's public address the start was realized
	// with; empty where the platform serves Managed Works none. A Work with
	// one opens its environment with platform_api and is delivered it as
	// its api credential, so both always agree.
	APIAddress  string            `json:"apiAddress,omitempty"`
	Environment map[string]string `json:"environment"`
	Telemetry   managedTelemetry  `json:"telemetry"`
}

// decodeManagedSpecification reads a Managed allocation's recorded
// environment.
func decodeManagedSpecification(record allocationRecord) (managedSpecification, error) {
	var spec managedSpecification
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return spec, oops.With("operation", "decode_managed_spec").Wrap(err)
	}
	for _, required := range []struct {
		field  string
		absent bool
	}{{"computer", spec.ComputerID == ""}, {"access_token", spec.AccessToken == ""}, {"telemetry_token", spec.Telemetry.Token == ""}} {
		if required.absent {
			return spec, oops.With("operation", "decode_managed_spec", "missing", required.field).Wrap(domain.ErrExecutionRequest)
		}
	}
	return spec, nil
}

// managedSpecificationFor is what a Managed start's environment holds. The
// machine supplies HOME, the bundled Claude Code on PATH and
// TALGENT_WORK_ROOT, and the Harness uses the machine's own git credentials:
// no mounts, no egress Vault, no CA bundle and no credential placeholders.
// Without read-only mounts, TALGENT_PROJECT_ID tells the runtime plugin a
// Work of a Project, which reads its Project's reference files through the
// platform Connector; TALGENT_KNOWLEDGE_ROOT is never set. On
// the machine's own Claude login nothing model-related is set. With a Model
// Configuration it holds the Model Template's environment except its
// endpoint: Talgent Computer's local proxy sets ANTHROPIC_BASE_URL to itself,
// and the run's proxy token in place of the key, for each process, and sends
// the requests on to the endpoint recorded here.
func managedSpecificationFor(request domain.StartExecutionRequest, models *ModelCatalog, token, credential string) (managedSpecification, error) {
	if strings.TrimSpace(request.ComputerID) == "" || strings.TrimSpace(token) == "" || strings.TrimSpace(credential) == "" {
		return managedSpecification{}, domain.ErrExecutionRequest
	}
	spec := managedSpecification{ComputerID: request.ComputerID, AccessToken: token, WorkCredential: credential, Environment: map[string]string{
		"TALGENT_WORK_ID": request.WorkID,
		// The bundled Claude Code follows the platform's release.
		"DISABLE_AUTOUPDATER": "1",
	}}
	if request.ProjectID != "" {
		spec.Environment["TALGENT_PROJECT_ID"] = request.ProjectID
	}
	if request.WorkModel.MachineLogin {
		return spec, nil
	}
	model, provided := models.Resolve(request.WorkModel.ConfigurationID)
	if !provided {
		return managedSpecification{}, errModelConfigurationNotProvided
	}
	modelEnvironment(spec.Environment, model.Template)
	delete(spec.Environment, "ANTHROPIC_BASE_URL")
	spec.ModelEndpoint = model.Template.BaseURL
	return spec, nil
}

// endpoint is the relay's execd base URL of one Work on its Computer, used
// for Agent's execd calls and handed to Dispatcher.
func (m *ManagedComputers) endpoint(spec managedSpecification, workID string) string {
	return m.relay.JoinPath("relay", spec.ComputerID, "works", workID).String()
}

// errMachineUnreachable means the relay holds no connection of the machine
// now. It is not the loss of the Work's environment.
var errMachineUnreachable = errors.New("agent: the Managed Computer is not reachable now")

// errWorkUnknown means the machine holds nothing of the Work: its
// reconciliation removed it, so the Work's environment there is gone.
var errWorkUnknown = errors.New("agent: the Managed Computer holds nothing of the Work")

// open opens, or repeats opening, the Work's environment on its machine. The
// machine keeps the Work's directory and replaces the token and environment.
func (m *ManagedComputers) open(ctx context.Context, spec managedSpecification, workID string) error {
	// A Work on a Model Configuration has its model reached through the
	// local proxy always, delivered or not, so it never falls back to the
	// machine's own Claude login. With PlatformAPI the machine points the
	// Work's TALGENT_API_ADDRESS at its local proxy.
	body, err := json.Marshal(struct {
		AccessToken string            `json:"access_token"`
		Environment map[string]string `json:"environment"`
		ModelProxy  bool              `json:"model_proxy,omitempty"`
		PlatformAPI bool              `json:"platform_api,omitempty"`
		Telemetry   machineTelemetry  `json:"telemetry"`
	}{spec.AccessToken, spec.Environment, spec.ModelEndpoint != "", spec.APIAddress != "", spec.machineTelemetry()})
	if err != nil {
		return oops.Wrap(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, m.endpoint(spec, workID), bytes.NewReader(body))
	if err != nil {
		return oops.Wrap(err)
	}
	request.Header.Set("X-Talgent-Control-Token", m.token)
	request.Header.Set("Content-Type", "application/json")
	return m.do(ctx, request, "open_work_environment")
}

// reachedWork is the Work's environment as its machine answers a ping: the
// relay base of its execd calls, and whether the machine's current
// reconciliation round is decided.
type reachedWork struct {
	endpoint   string
	reconciled bool
}

// resolve finds the Work's environment on its machine: running while the
// relay reaches the machine and it holds the Work, lost once the machine
// holds nothing of the Work or Computer says it was removed, pending
// otherwise.
func (m *ManagedComputers) resolve(ctx context.Context, spec managedSpecification, workID string) (reachedWork, environmentState, error) {
	endpoint := m.endpoint(spec, workID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/ping", nil)
	if err != nil {
		return reachedWork{}, environmentPending, oops.Wrap(err)
	}
	request.Header.Set("X-EXECD-ACCESS-TOKEN", spec.AccessToken)
	body, err := m.call(ctx, request, "ping_work_environment")
	switch {
	case err == nil:
		// A machine that does not report its round leaves it undecided.
		var ping struct {
			Reconciled bool `json:"reconciled"`
		}
		_ = json.Unmarshal(body, &ping)
		return reachedWork{endpoint: endpoint, reconciled: ping.Reconciled}, environmentRunning, nil
	case errors.Is(err, errWorkUnknown):
		return reachedWork{}, environmentLost, nil
	case !errors.Is(err, errMachineUnreachable):
		return reachedWork{}, environmentPending, err
	}
	removed, describeErr := m.removed(ctx, spec)
	if describeErr != nil {
		return reachedWork{}, environmentPending, describeErr
	}
	if removed {
		return reachedWork{}, environmentLost, nil
	}
	return reachedWork{}, environmentPending, nil
}

// removed reports whether the Work's Computer was removed, which confirms the
// loss of its environment. A start still waiting in a removed Computer's line
// asks for no environment, so Agent's handling of Computer Removed and the
// Work's recovery end it instead (command.ComputerReachHandler).
func (m *ManagedComputers) removed(ctx context.Context, spec managedSpecification) (bool, error) {
	place, err := m.computers.DescribeComputer(ctx, spec.ComputerID)
	if err != nil {
		return false, err
	}
	return place.Removed, nil
}

// release reclaims a run's compute on its machine: it deletes the run's
// process handles, which kills what still runs and forgets their output. The
// Work's directory stays for its life. A removed machine has nothing left to
// release; an unreachable one is released once it is reachable again.
func (m *ManagedComputers) release(ctx context.Context, spec managedSpecification, workID string, processIDs ...string) (bool, error) {
	reached, state, err := m.resolve(ctx, spec, workID)
	switch {
	case err != nil:
		return false, err
	case state == environmentLost:
		// A removed machine, or a Work its reconciliation removed, holds no
		// process of the run.
		return true, nil
	case state == environmentPending:
		return false, nil
	}
	client, err := execd.NewClient(m.http, reached.endpoint, spec.AccessToken)
	if err != nil {
		return false, err
	}
	for _, id := range processIDs {
		if id == "" {
			continue
		}
		if err := client.Delete(ctx, id); err != nil {
			return false, err
		}
	}
	return true, nil
}

// do makes one relayed call of the Work. A 404 means the machine holds
// nothing of the Work (errWorkUnknown), which each caller applies: a ping
// finds the Work's environment lost, and a release or a withdrawal of its
// credentials finds nothing left to do.
func (m *ManagedComputers) do(ctx context.Context, request *http.Request, operation string) error {
	_, err := m.call(ctx, request, operation)
	return err
}

// relayAnswerLimit bounds a relayed answer read back; a longer one is cut.
const relayAnswerLimit = 64 * 1024

// call makes one relayed call of the Work and answers its body.
func (m *ManagedComputers) call(ctx context.Context, request *http.Request, operation string) ([]byte, error) {
	response, err := m.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, oops.With("operation", operation).Wrap(err)
	}
	defer response.Body.Close()
	// The body is read, and drained, so the connection can be reused.
	body, _ := io.ReadAll(io.LimitReader(response.Body, relayAnswerLimit))
	_, _ = io.Copy(io.Discard, response.Body)
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return body, nil
	case response.StatusCode == http.StatusServiceUnavailable:
		return nil, errMachineUnreachable
	case response.StatusCode == http.StatusNotFound:
		return nil, oops.With("operation", operation).Wrap(errWorkUnknown)
	default:
		// Relay bodies may echo the Work's environment.
		return nil, oops.With("operation", operation, "status", response.StatusCode).Errorf("agent: relay returned HTTP %d", response.StatusCode)
	}
}
