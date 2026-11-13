package application

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/account/domain"
)

// IssueWorkIdentityToken names a Work and the fixed facts Agent supplies for
// it.
type IssueWorkIdentityToken struct {
	WorkID            string
	WorkCallerKind    string
	WorkCallerID      string
	WorkKeyKind       string
	WorkKeyID         string
	FileHomeKind      string
	FileHomeID        string
	AgentID           string
	Purpose           string
	RepresentedPerson string
	OwningApplication string
	WorkLabels        map[string]string
}

// IssuedToken is a signed Identity Token and its expiry.
type IssuedToken struct {
	Token string
	// ExpiresAt is when the token stops being valid.
	ExpiresAt time.Time
}

// IssueWorkIdentityToken issues the Work's agent Identity Token, opening its
// Agent Account on the first request. A request whose facts differ from the
// Agent Account's is refused with ErrAgentAccountFactsDiffer. Account stores
// nothing of the token.
func (a *Application) IssueWorkIdentityToken(ctx context.Context, cmd IssueWorkIdentityToken) (IssuedToken, error) {
	work, err := domain.ParseWorkID(cmd.WorkID)
	if err != nil {
		return IssuedToken{}, err
	}
	caller, err := domain.NewWorkCaller(cmd.WorkCallerKind, cmd.WorkCallerID)
	if err != nil {
		return IssuedToken{}, err
	}
	key, err := domain.NewWorkKey(cmd.WorkKeyKind, cmd.WorkKeyID)
	if err != nil {
		return IssuedToken{}, err
	}
	home, err := domain.NewFileHome(cmd.FileHomeKind, cmd.FileHomeID)
	if err != nil {
		return IssuedToken{}, err
	}
	facts, err := domain.NewWorkFacts(caller, key, home, cmd.AgentID, cmd.Purpose, cmd.RepresentedPerson, cmd.OwningApplication, cmd.WorkLabels)
	if err != nil {
		return IssuedToken{}, err
	}
	account, err := a.agentAccount(ctx, work, facts)
	if err != nil {
		return IssuedToken{}, err
	}
	claims, err := account.IssueIdentityToken(facts, a.now(), a.agentTokenLifetime)
	if err != nil {
		return IssuedToken{}, err
	}
	token, err := a.tokens.Sign(ctx, claims)
	if err != nil {
		return IssuedToken{}, err
	}
	return IssuedToken{Token: token, ExpiresAt: claims.ExpiresAt}, nil
}

// agentAccount finds the Work's Agent Account or opens it; a concurrent first
// request that opened it meanwhile is read back (Open).
func (a *Application) agentAccount(ctx context.Context, work domain.WorkID, facts domain.WorkFacts) (*domain.AgentAccount, error) {
	account, err := a.agentAccounts.Get(ctx, work)
	if err == nil || !errors.Is(err, domain.ErrAgentAccountNotFound) {
		return account, err
	}
	account, err = domain.OpenAgentAccount(work, facts, a.now())
	if err != nil {
		return nil, err
	}
	err = a.agentAccounts.Save(ctx, account)
	if errors.Is(err, domain.ErrAgentAccountExists) {
		return a.agentAccounts.Get(ctx, work)
	}
	return account, err
}
