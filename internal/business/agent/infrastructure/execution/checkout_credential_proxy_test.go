package execution

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Checkout holds no credential: the preparation process clones with only the
// planned URL, and the network path between the sandbox and the provider (the
// sandbox's Credential Proxy) adds the Work's credential. A forward proxy that
// injects it stands in for the Credential Proxy here.
func TestCheckoutReachesAPrivateRepositoryOnlyThroughTheCredentialProxy(t *testing.T) {
	requireNodeAndGit(t)
	remotes := t.TempDir()
	gitRemote(t, remotes, "private", "main")
	gitBinary, err := exec.LookPath("git")
	require.NoError(t, err)
	backend := &cgi.Handler{Path: gitBinary, Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + remotes, "GIT_HTTP_EXPORT_ALL=1"}}
	credential := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:work-credential-fixture"))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != credential {
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			http.Error(w, "credential required", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(provider.Close)
	upstream, err := url.Parse(provider.URL)
	require.NoError(t, err)
	proxy := httptest.NewServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.Out.URL.Scheme, r.Out.URL.Host = upstream.Scheme, upstream.Host
		r.Out.Header.Set("Authorization", credential)
	}})
	t.Cleanup(proxy.Close)
	plan := []repositoryCheckout{{RepositoryID: "github:1", CloneURL: "http://github.fixture.invalid/private.git", BaseBranch: "main", WorkBranch: "talgent/TAL-1", TargetPath: "/workspace/repos/private"}}

	// Without the proxy the process reaches the provider itself, which refuses
	// it: it has no credential to offer. (The provider's own address, not the
	// fixture host, so the check never waits on name resolution.)
	direct := t.TempDir()
	directPlan := []repositoryCheckout{plan[0]}
	directPlan[0].CloneURL = provider.URL + "/private.git"
	require.Equal(t, domain.CheckoutUnavailable, runCheckout(t, direct, directPlan, "").checkouts(directPlan, domain.CheckoutIncomplete)[0].Reason, "without the proxy the process has no credential to offer")
	require.NoDirExists(t, filepath.Join(direct, "repos", "private"))

	workspace := t.TempDir()
	require.True(t, runCheckout(t, workspace, plan, proxy.URL).checkouts(plan, domain.CheckoutIncomplete)[0].CheckedOut)
	require.Equal(t, "talgent/TAL-1", gitIn(t, filepath.Join(workspace, "repos", "private"), "rev-parse", "--abbrev-ref", "HEAD"))
	config, err := os.ReadFile(filepath.Join(workspace, "repos", "private", ".git", "config"))
	require.NoError(t, err)
	require.NotContains(t, string(config), "work-credential-fixture", "the checkout retains no credential")
}

// runCheckout runs the preparation process with only proxy settings beyond a
// clean environment, so no credential can reach it any other way.
func runCheckout(t *testing.T, workspace string, plan []repositoryCheckout, proxy string) preparationReport {
	t.Helper()
	input := request()
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	encodedPlan := string(encoded)
	process := exec.Command("node", "-e", nativePreparation, "claude", `{"name":"talgent-work-fixture","owner":{"name":"Talgent"},"plugins":[]}`, storageKey(input.WorkID + ":" + fixtureAllocationID)[:32], encodedPlan, "60")
	process.Dir = workspace
	process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_GLOBAL=/dev/null", "NO_PROXY=", "no_proxy="}
	if proxy != "" {
		process.Env = append(process.Env, "http_proxy="+proxy, "HTTP_PROXY="+proxy)
	}
	output, err := process.Output()
	require.NoError(t, err)
	report := latestReport(output, 0, domain.CapabilitySnapshot{}, plan)
	require.NotNil(t, report.Repositories, "%s", output)
	return report
}
