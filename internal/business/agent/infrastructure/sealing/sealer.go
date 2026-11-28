// Package sealing keeps Personal Variable values encrypted at rest under the
// Agent's own key, so the database alone never yields one: in each person's
// PersonalVariableSet and in each AgentExecution that took them at its start.
package sealing

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// KeySize is the length of the Agent key: AES-256.
const KeySize = 32

// errTruncated means a stored value is shorter than its nonce.
var errTruncated = errors.New("agent: sealed Personal Variables are truncated")

// Sealer encrypts Personal Variables with AES-256-GCM. Each sealed value is
// bound to what it belongs to, so it cannot be moved to another person or
// another execution and still open.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer takes the Agent key, KeySize bytes.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != KeySize {
		return nil, errors.New("agent: the Personal Variable key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Binding names what a sealed value belongs to; it is authenticated, never
// stored.
type Binding string

// Person binds a person's PersonalVariableSet.
func Person(personID string) Binding { return Binding("personal-variable-set/" + personID) }

// Execution binds the Personal Variables an AgentExecution took at its start.
func Execution(executionID string) Binding { return Binding("agent-execution/" + executionID) }

// sealedVariable is one Personal Variable inside a sealed value.
type sealedVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Seal encrypts the variables for binding.
func (s *Sealer) Seal(variables []domain.PersonalVariable, binding Binding) ([]byte, error) {
	plain := make([]sealedVariable, len(variables))
	for i, variable := range variables {
		plain[i] = sealedVariable{Name: variable.Name, Value: variable.Value}
	}
	encoded, err := json.Marshal(plain)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, encoded, []byte(binding)), nil
}

// Open decrypts variables sealed for binding. Its errors never carry the
// plaintext.
func (s *Sealer) Open(sealed []byte, binding Binding) ([]domain.PersonalVariable, error) {
	size := s.aead.NonceSize()
	if len(sealed) < size {
		return nil, errTruncated
	}
	encoded, err := s.aead.Open(nil, sealed[:size], sealed[size:], []byte(binding))
	if err != nil {
		return nil, errors.New("agent: sealed Personal Variables do not open under the Agent key")
	}
	var plain []sealedVariable
	if err := json.Unmarshal(encoded, &plain); err != nil {
		return nil, errors.New("agent: sealed Personal Variables are malformed")
	}
	variables := make([]domain.PersonalVariable, len(plain))
	for i, variable := range plain {
		variables[i] = domain.PersonalVariable{Name: variable.Name, Value: variable.Value}
	}
	return variables, nil
}
