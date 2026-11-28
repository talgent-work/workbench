package execd_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// A creation with variables carries them in personal_variables and succeeds
// only once the endpoint acknowledges them; one that ignores them, as
// OpenSandbox execd or an earlier Talgent Computer, answers the reserved
// handle with ErrVariablesIgnored so the caller deletes it.
func TestCreateWithVariablesNeedsTheirAcknowledgement(t *testing.T) {
	answer := `{"session_id":"process","personal_variables":true}`
	var sent []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST /pty", r.Method+" "+r.URL.Path)
		sent, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, answer)
	}))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	variables := map[string]string{"API_TOKEN": "secret-value", "PEM": "a\nb"}

	id, err := client.CreateWithVariables(context.Background(), "/workspace", "exec claude", variables)
	require.NoError(t, err)
	require.Equal(t, "process", id)
	var body struct {
		Cwd       string            `json:"cwd"`
		Command   string            `json:"command"`
		Variables map[string]string `json:"personal_variables"`
	}
	require.NoError(t, json.Unmarshal(sent, &body))
	require.Equal(t, "/workspace", body.Cwd)
	require.Equal(t, "exec claude", body.Command)
	require.Equal(t, variables, body.Variables)
	size, err := execd.CreationBytes("/workspace", "exec claude", variables)
	require.NoError(t, err)
	require.Equal(t, len(sent), size, "the body as sent")

	answer = `{"session_id":"process"}`
	id, err = client.CreateWithVariables(context.Background(), "/workspace", "exec claude", variables)
	require.ErrorIs(t, err, execd.ErrVariablesIgnored)
	require.Equal(t, "process", id, "the ignored creation's handle, for its deletion")
	require.NotContains(t, err.Error(), "secret-value")
}
