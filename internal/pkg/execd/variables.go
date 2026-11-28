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

// VariableCreation is a process reserved with variables: its handle, and the
// names of the variables of its environment that they replace there, which
// Talgent Computer names because it composes that environment.
type VariableCreation struct {
	ID       string
	Replaced []string
}

// CreateWithVariables reserves a process like Create, whose command Talgent
// Computer starts with variables layered over every other variable of its
// environment (personal_variables). Only Talgent Computer serves them, and
// it acknowledges them in its answer; an answer without the acknowledgement
// returns the reserved handle with ErrVariablesIgnored. Neither its errors
// nor the request's log carry a value.
func (c *Client) CreateWithVariables(ctx context.Context, cwd, command string, variables map[string]string) (VariableCreation, error) {
	if variables == nil {
		return VariableCreation{}, errors.New("execd: working directory, command and variables required")
	}
	answer, err := c.create(ctx, cwd, command, variables)
	if err != nil {
		return VariableCreation{}, err
	}
	created := VariableCreation{ID: answer.SessionID, Replaced: answer.Replaced}
	if !answer.Variables {
		return created, ErrVariablesIgnored
	}
	return created, nil
}

// CreationBytes is the size of the request body CreateWithVariables sends,
// for a caller whose endpoint bounds it.
func CreationBytes(cwd, command string, variables map[string]string) (int, error) {
	body, err := json.Marshal(newCreation(cwd, command, variables))
	return len(body), err
}

// AwaitsVariables reports whether the process was reserved with variables
// that its Talgent Computer no longer holds, as after a restart, and has
// never run: it neither starts nor fails until RestoreVariables gives them
// again. An unknown process awaits nothing.
func (c *Client) AwaitsVariables(ctx context.Context, id string) (bool, error) {
	if !validID(id) {
		return false, errors.New("execd: invalid process handle")
	}
	body, err := c.request(ctx, http.MethodGet, c.endpoint("pty", id), nil)
	var response *responseError
	if errors.As(err, &response) && response.status == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var status struct {
		SessionID string `json:"session_id"`
		Awaiting  bool   `json:"awaiting_personal_variables"`
	}
	if json.Unmarshal(body, &status) != nil || status.SessionID != id {
		return false, errors.New("execd: invalid process status")
	}
	return status.Awaiting, nil
}

// RestoreVariables gives a process reserved with variables, which has never
// run, its variables again. Neither its errors nor the request's log carry a
// value.
func (c *Client) RestoreVariables(ctx context.Context, id string, variables map[string]string) error {
	if !validID(id) || variables == nil {
		return errors.New("execd: process handle and variables required")
	}
	_, err := c.request(ctx, http.MethodPut, c.endpoint("pty", id, "personal_variables"), struct {
		Variables map[string]string `json:"personal_variables"`
	}{variables})
	return err
}

// creation is a process creation, with variables only where Talgent
// Computer serves them: Variables holds a map, an empty one included, or
// nil for none.
type creation struct {
	Cwd       string `json:"cwd"`
	Command   string `json:"command"`
	Variables any    `json:"personal_variables,omitempty"`
}

func newCreation(cwd, command string, variables map[string]string) creation {
	if variables == nil {
		return creation{Cwd: cwd, Command: command}
	}
	return creation{Cwd: cwd, Command: command, Variables: variables}
}

// creationAnswer is a process creation's answer.
type creationAnswer struct {
	SessionID string   `json:"session_id"`
	Variables bool     `json:"personal_variables"`
	Replaced  []string `json:"replaced_variables"`
}

// create reserves a process, with variables when they are not nil.
func (c *Client) create(ctx context.Context, cwd, command string, variables map[string]string) (creationAnswer, error) {
	if strings.TrimSpace(cwd) == "" || strings.TrimSpace(command) == "" {
		return creationAnswer{}, errors.New("execd: working directory and command required")
	}
	body, err := c.request(ctx, http.MethodPost, c.endpoint("pty"), newCreation(cwd, command, variables))
	if err != nil {
		return creationAnswer{}, err
	}
	var answer creationAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return creationAnswer{}, oops.With("operation", "create").Wrap(err)
	}
	if !validID(answer.SessionID) {
		return creationAnswer{}, errors.New("execd: invalid process handle")
	}
	return answer, nil
}
