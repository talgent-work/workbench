package agent

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/sealing"
)

// PersonalVariableConfig locates the Agent key that seals Personal Variables
// at rest. The deployment provides it once and keeps it: values sealed under
// a lost key cannot be opened again.
type PersonalVariableConfig struct {
	// KeyPath is a file of 32 base64-encoded bytes.
	KeyPath string `json:"key-path" yaml:"key-path" toml:"key-path"`
}

func (c PersonalVariableConfig) Validate() error {
	if strings.TrimSpace(c.KeyPath) == "" {
		return errors.New("agent personal variables config: key-path is required")
	}
	return nil
}

// newPersonalVariableSealer reads the Agent key; the controlplane does not
// start without a valid one.
func newPersonalVariableSealer(config PersonalVariableConfig) (*sealing.Sealer, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(strings.TrimSpace(config.KeyPath))
	if err != nil {
		return nil, fmt.Errorf("agent personal variable key: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != sealing.KeySize {
		return nil, errors.New("agent personal variable key must be 32 base64-encoded bytes")
	}
	return sealing.NewSealer(key)
}
