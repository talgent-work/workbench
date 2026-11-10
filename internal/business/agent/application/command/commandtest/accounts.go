package commandtest

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// AgentTokenLifetime is how long the tokens Accounts issues stay valid, as
// Account's agent token lifetime.
const AgentTokenLifetime = 7 * 24 * time.Hour

// ErrTokenRefused is what Accounts answers while Refuse is set.
var ErrTokenRefused = errors.New("commandtest: account refused the identity token")

// Accounts is Account as Agent reaches it: an empty User Directory, and an
// Agent Account Command issuing a fresh token per request, valid
// AgentTokenLifetime from Now, or refusing while Refuse is set.
type Accounts struct {
	Now    func() time.Time
	Refuse bool

	mu     sync.Mutex
	issued []command.WorkIdentityFacts
}

var _ command.AccountPort = (*Accounts)(nil)

func (*Accounts) SearchUsers(context.Context, string, int) ([]command.DirectoryUser, error) {
	return nil, nil
}

func (*Accounts) GetUsers(context.Context, []string) ([]command.DirectoryUser, error) {
	return nil, nil
}

func (a *Accounts) IssueWorkIdentityToken(_ context.Context, facts command.WorkIdentityFacts) (command.IdentityToken, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Refuse {
		return command.IdentityToken{}, ErrTokenRefused
	}
	a.issued = append(a.issued, facts)
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	return command.IdentityToken{Token: "agent-token-" + facts.WorkID, ExpiresAt: now().Add(AgentTokenLifetime)}, nil
}

// Issued lists the facts of every token issued so far.
func (a *Accounts) Issued() []command.WorkIdentityFacts {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]command.WorkIdentityFacts(nil), a.issued...)
}

// Definitions answers every AgentDefinition as one of purpose Purpose, for
// supplies that read a definition's purpose only.
type Definitions struct{ Purpose string }

var _ domain.AgentDefinitionRepository = Definitions{}

func (d Definitions) Get(_ context.Context, id string) (*domain.AgentDefinition, error) {
	purpose := d.Purpose
	if purpose == "" {
		purpose = "test-purpose"
	}
	return &domain.AgentDefinition{ID: id, Purpose: purpose}, nil
}

func (Definitions) Save(context.Context, *domain.AgentDefinition) error {
	return errors.New("commandtest: definitions are read-only")
}
