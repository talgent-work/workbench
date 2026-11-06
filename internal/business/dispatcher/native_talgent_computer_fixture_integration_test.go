//go:build integration

package dispatcher_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	computerprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/computer/private/v1/computerprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/computergateway"
	"github.com/talgent-work/workbench/internal/pkg/computerconnection"
)

// talgentComputerProgram, when set to Talgent Computer's built program
// (apps/computer/dist/talgent-computer.mjs), runs the native cases against it
// through the Computer Gateway instead of against execd directly.
const talgentComputerProgram = "TALGENT_COMPUTER_PROGRAM"

const fixtureComputer, fixtureWork = "c-fixture", "w-fixture"

// fixtureComputerService authenticates the fixture's one Managed Computer.
type fixtureComputerService struct {
	computerprivatev1connect.UnimplementedConnectionServiceHandler
}

func (fixtureComputerService) Authenticate(_ context.Context, req *connect.Request[computerprivatev1.AuthenticateRequest]) (*connect.Response[computerprivatev1.AuthenticateResponse], error) {
	if req.Msg.GetCredential() != "fixture-credential" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("rejected"))
	}
	return connect.NewResponse(&computerprivatev1.AuthenticateResponse{ComputerId: fixtureComputer}), nil
}

// ReportReachable announces that the platform reconciles the machine, which
// reconcileFixtureMachine then does.
func (fixtureComputerService) ReportReachable(context.Context, *connect.Request[computerprivatev1.ReportReachableRequest]) (*connect.Response[computerprivatev1.ReportReachableResponse], error) {
	return connect.NewResponse(&computerprivatev1.ReportReachableResponse{Reconciles: true}), nil
}

func (fixtureComputerService) ReportUnreachable(context.Context, *connect.Request[computerprivatev1.ReportUnreachableRequest]) (*connect.Response[computerprivatev1.ReportUnreachableResponse], error) {
	return connect.NewResponse(&computerprivatev1.ReportUnreachableResponse{}), nil
}

// managedHarness is how the native cases run Claude Code on Talgent
// Computer: by name, so the Claude Code bundled with it, first on PATH, is
// the one found.
const managedHarness = "claude"

// claudeCodeVersion is what a Claude Code executable answers to --version.
func claudeCodeVersion(t *testing.T, binary string) string {
	t.Helper()
	output, err := exec.Command(binary, "--version").Output()
	require.NoError(t, err)
	version, _, _ := strings.Cut(strings.TrimSpace(string(output)), " ")
	require.NotEmpty(t, version)
	return version
}

// startTalgentComputer runs Talgent Computer connected to an in-process
// Computer Gateway and opens one Work whose processes get env. The Claude
// Code Talgent Computer bundles is claudeBinary's version, the one the
// sandbox ships. It returns the Work's relayed execd endpoint, which serves
// token.
func startTalgentComputer(t *testing.T, ctx context.Context, program, claudeBinary, root string, env []string, token string) string {
	t.Helper()
	// The program is <package>/dist/talgent-computer.mjs; npm links the
	// bundled Claude Code into <package>/node_modules/.bin.
	bundled := filepath.Join(filepath.Dir(filepath.Dir(program)), "node_modules", ".bin", managedHarness)
	require.Equal(t, claudeCodeVersion(t, claudeBinary), claudeCodeVersion(t, bundled), "Talgent Computer bundles the sandbox's Claude Code")
	backendMux := http.NewServeMux()
	backendMux.Handle(computerprivatev1connect.NewConnectionServiceHandler(fixtureComputerService{}))
	backend := httptest.NewServer(backendMux)
	t.Cleanup(backend.Close)
	rdb := goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()})
	server := httptest.NewUnstartedServer(nil)
	gateway := computergateway.NewGateway(computerprivatev1connect.NewConnectionServiceClient(http.DefaultClient, backend.URL),
		computerconnection.NewWriter(rdb), computerconnection.NewReader(rdb),
		computergateway.Replica{Name: "fixture", Endpoint: server.Listener.Addr().String(), ControlToken: "fixture-control"}, slog.Default())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /computer/v1/connect", gateway.ServeConnect)
	mux.HandleFunc("/relay/{computer}/works/{work}", gateway.ServeRelay)
	mux.HandleFunc("/relay/{computer}/works/{work}/{rest...}", gateway.ServeRelay)
	mux.HandleFunc("/relay/{computer}/reconciliation", gateway.ServeMachineRelay)
	server.Config.Handler = mux
	server.Start()
	t.Cleanup(server.Close)

	machine := filepath.Join(root, "machine")
	config := filepath.Join(machine, "config", "talgent-computer")
	require.NoError(t, os.MkdirAll(config, 0o700))
	credential, err := json.Marshal(map[string]any{"server": server.URL, "computerId": fixtureComputer, "name": "fixture", "credential": "fixture-credential", "credentialExpiresAt": nil})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(config, "credential.json"), credential, 0o600))
	log, err := os.Create(filepath.Join(root, "talgent-computer.log"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = log.Close() })
	run := exec.Command("node", program, "run")
	run.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "XDG_CONFIG_HOME=" + filepath.Join(machine, "config"), "XDG_DATA_HOME=" + filepath.Join(machine, "data")}
	run.Stdout, run.Stderr = log, log
	require.NoError(t, run.Start())
	t.Cleanup(func() { _ = run.Process.Signal(os.Interrupt); _ = run.Wait() })

	// The Work's model is a Model Configuration: its endpoint and key reach
	// the machine as Work credentials, and the Harness reaches the model only
	// through Talgent Computer's local proxy, which must add the key.
	environment, inference := map[string]string{}, ""
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "PATH", "HOME", "EXECD_ACCESS_TOKEN", "ANTHROPIC_API_KEY":
		case "ANTHROPIC_BASE_URL":
			inference = value
		default:
			environment[name] = value
		}
	}
	require.NotEmpty(t, inference)
	body, err := json.Marshal(map[string]any{"access_token": token, "environment": environment, "model_proxy": true})
	require.NoError(t, err)
	base := server.URL + "/relay/" + fixtureComputer + "/works/" + fixtureWork
	require.Eventually(t, func() bool {
		request, _ := http.NewRequestWithContext(ctx, http.MethodPut, base, strings.NewReader(string(body)))
		request.Header.Set(computergateway.HeaderControlToken, "fixture-control")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, 30*time.Second, 100*time.Millisecond, "Talgent Computer connects and opens the Work")
	credentials, err := json.Marshal(map[string]any{
		"model":   map[string]string{"base_url": keyedModel(t, inference), "key": fixtureModelKey},
		"gateway": map[string]string{"url": "http://127.0.0.1:1/mcp", "work_id": fixtureWork, "work_credential": "fixture-work-credential"},
	})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/credentials", strings.NewReader(string(credentials)))
	require.NoError(t, err)
	request.Header.Set(computergateway.HeaderControlToken, "fixture-control")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, http.StatusNoContent, response.StatusCode, "Talgent Computer takes the Work's credentials")
	reconcileFixtureMachine(t, ctx, server.URL+"/relay/"+fixtureComputer+"/reconciliation")
	return base
}

// reconcileFixtureMachine decides the connection's reconciliation round as
// Agent does for a machine that holds nothing to end, so its processes start.
func reconcileFixtureMachine(t *testing.T, ctx context.Context, endpoint string) {
	t.Helper()
	call := func(method, body string) *http.Response {
		request, err := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(body))
		require.NoError(t, err)
		request.Header.Set(computergateway.HeaderControlToken, "fixture-control")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		return response
	}
	response := call(http.MethodGet, "")
	var round struct {
		Round string `json:"round"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&round))
	_ = response.Body.Close()
	decision, err := json.Marshal(map[string]any{"round": round.Round})
	require.NoError(t, err)
	response = call(http.MethodPost, string(decision))
	_ = response.Body.Close()
	require.Equal(t, http.StatusNoContent, response.StatusCode, "Talgent Computer applies the reconciliation")
}

const fixtureModelKey = "fixture-model-key"

// keyedModel fronts the fake inference with the key check a vendor makes: a
// model request without the Model Configuration's key, which only Talgent
// Computer's local proxy adds, is refused.
func keyedModel(t *testing.T, inference string) string {
	t.Helper()
	target, err := url.Parse(inference)
	require.NoError(t, err)
	forward := httputil.NewSingleHostReverseProxy(target)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/messages") && r.Header.Get("Authorization") != "Bearer "+fixtureModelKey {
			http.Error(w, "model key required", http.StatusUnauthorized)
			return
		}
		forward.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	return front.URL
}
