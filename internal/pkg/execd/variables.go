package execd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/samber/oops"
)

// ErrVariablesIgnored means the process was reserved without the variables
// sent with its creation: the endpoint, an OpenSandbox execd or a Talgent
// Computer that predates them, does not acknowledge them, and would start
// the command without them. The caller deletes the returned handle.
var ErrVariablesIgnored = errors.New("execd: the process creation did not take its variables")

// CreateWithVariables reserves a process like Create, whose command Talgent
// Computer starts with variables layered over every other variable of its
// environment (personal_variables). Only Talgent Computer serves them, and
// it acknowledges them in its answer; an answer without the acknowledgement
// returns the reserved handle with ErrVariablesIgnored. Neither its errors
// nor the request's log carry a value.
func (c *Client) CreateWithVariables(ctx context.Context, cwd, command string, variables map[string]string) (string, error) {
	if strings.TrimSpace(cwd) == "" || strings.TrimSpace(command) == "" || variables == nil {
		return "", errors.New("execd: working directory, command and variables required")
	}
	body, err := c.request(ctx, http.MethodPost, c.endpoint("pty"), variableCreation{cwd, command, variables})
	if err != nil {
		return "", err
	}
	var result struct {
		SessionID string `json:"session_id"`
		Variables bool   `json:"personal_variables"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", oops.With("operation", "create").Wrap(err)
	}
	if !validID(result.SessionID) {
		return "", errors.New("execd: invalid process handle")
	}
	if !result.Variables {
		return result.SessionID, ErrVariablesIgnored
	}
	return result.SessionID, nil
}

// CreationBytes is the size of the request body CreateWithVariables sends,
// for a caller whose endpoint bounds it.
func CreationBytes(cwd, command string, variables map[string]string) (int, error) {
	body, err := json.Marshal(variableCreation{cwd, command, variables})
	return len(body), err
}

// variableCreation is Talgent Computer's process creation with variables.
type variableCreation struct {
	Cwd       string            `json:"cwd"`
	Command   string            `json:"command"`
	Variables map[string]string `json:"personal_variables"`
}
