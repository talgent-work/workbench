package computergateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheReleaseIsAnsweredForTheInstallCommand(t *testing.T) {
	for version, body := range map[string]string{"1.42.0": `{"version":"1.42.0"}`, "": `{"version":""}`} {
		recorder := httptest.NewRecorder()
		Release{Version: version}.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/computer/v1/release", nil))
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
		require.JSONEq(t, body, recorder.Body.String())
	}
}

func TestTheReleaseVersionIsAPlainVersion(t *testing.T) {
	valid := Config{ComputerAddress: "http://computer", ControlToken: "t", Replica: "r", Endpoint: "e"}
	require.NoError(t, valid.validate(), "no release outside a release deployment")
	for version, ok := range map[string]bool{"0.31.0": true, "v0.31.0": false, "release-458-abc1234": false, "1.2": false} {
		config := valid
		config.ReleaseVersion = version
		require.Equal(t, ok, config.validate() == nil, version)
	}
}

func TestAMinimumAboveTheTargetIsAConflict(t *testing.T) {
	for _, c := range []struct {
		target, minimum string
		conflict        bool
	}{
		{"0.3.0", "0.4.0", true},
		{"0.3.0", "0.10.0", true},
		{"0.3.0", "0.3.0", false},
		{"0.3.0", "0.2.0", false},
		{"0.3.0", "", false},
		{"", "0.4.0", false},
	} {
		require.Equal(t, c.conflict, minimumAboveTarget(c.target, c.minimum), "%s/%s", c.target, c.minimum)
	}
}
