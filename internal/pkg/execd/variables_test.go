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
	answer := `{"session_id":"process","personal_variables":true,"replaced_variables":["HOME","PATH"]}`
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

	created, err := client.CreateWithVariables(context.Background(), "/workspace", "exec claude", variables)
	require.NoError(t, err)
	require.Equal(t, execd.VariableCreation{ID: "process", Replaced: []string{"HOME", "PATH"}}, created)
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

	_, err = client.CreateWithVariables(context.Background(), "/workspace", "exec claude", map[string]string{})
	require.NoError(t, err)
	require.JSONEq(t, `{"cwd":"/workspace","command":"exec claude","personal_variables":{}}`, string(sent), "an empty set is still sent")

	answer = `{"session_id":"process"}`
	created, err = client.CreateWithVariables(context.Background(), "/workspace", "exec claude", variables)
	require.ErrorIs(t, err, execd.ErrVariablesIgnored)
	require.Equal(t, "process", created.ID, "the ignored creation's handle, for its deletion")
	require.NotContains(t, err.Error(), "secret-value")
}

// A process that lost its variables before it ever ran says so in its
// status, and takes them again; neither call carries a value in an error.
func TestAProcessAwaitingItsVariablesTakesThemAgain(t *testing.T) {
	status := `{"session_id":"process","running":false,"awaiting_personal_variables":true}`
	restored := http.StatusNoContent
	var sent []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /pty/process":
			_, _ = io.WriteString(w, status)
		case "PUT /pty/process/personal_variables":
			sent, _ = io.ReadAll(r.Body)
			w.WriteHeader(restored)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	ctx := context.Background()

	awaiting, err := client.AwaitsVariables(ctx, "process")
	require.NoError(t, err)
	require.True(t, awaiting)
	status = `{"session_id":"process","running":true}`
	awaiting, err = client.AwaitsVariables(ctx, "process")
	require.NoError(t, err)
	require.False(t, awaiting)
	awaiting, err = client.AwaitsVariables(ctx, "gone")
	require.NoError(t, err)
	require.False(t, awaiting, "an unknown process awaits nothing")

	variables := map[string]string{"API_TOKEN": "secret-value"}
	require.NoError(t, client.RestoreVariables(ctx, "process", variables))
	require.JSONEq(t, `{"personal_variables":{"API_TOKEN":"secret-value"}}`, string(sent))
	restored = http.StatusConflict
	err = client.RestoreVariables(ctx, "process", variables)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-value")
}
