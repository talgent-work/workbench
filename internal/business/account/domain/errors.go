package domain

import "errors"

var (
	ErrInvalidAccount         = errors.New("invalid account")
	ErrInvalidProfile         = errors.New("invalid profile")
	ErrAccountNotFound        = errors.New("account not found")
	ErrGitHubBindingTaken     = errors.New("github user already has an account")
	ErrGitHubBindingMismatch  = errors.New("sign-in is for another github user")
	ErrInvalidLoginSession    = errors.New("invalid login session")
	ErrLoginSessionNotFound   = errors.New("login session not found")
	ErrLoginSessionRevoked    = errors.New("login session revoked")
	ErrLoginSessionExpired    = errors.New("login session expired")
	ErrRefreshSecretReused    = errors.New("refresh secret already replaced")
	ErrRefreshSecretUnknown   = errors.New("refresh secret not held by session")
	ErrConcurrentModification = errors.New("concurrent modification")
	ErrInvalidAgentAccount    = errors.New("invalid agent account")
	ErrAgentAccountNotFound   = errors.New("agent account not found")
	// ErrAgentAccountExists means another request opened the Work's Agent
	// Account first.
	ErrAgentAccountExists      = errors.New("agent account already exists")
	ErrAgentAccountFactsDiffer = errors.New("work facts differ from the agent account")
	ErrInvalidApplication      = errors.New("invalid application")
	// ErrApplicationNotFound also answers a person who does not own the
	// Application, so its existence stays private.
	ErrApplicationNotFound = errors.New("application not found")
	// ErrClientSecretMismatch means no valid Client Secret of an active
	// Application accepts the presented secret.
	ErrClientSecretMismatch = errors.New("client secret does not match")
	// ErrClientSecretNotFound names no Client Secret of the Application that
	// still matches, so there is nothing to revoke.
	ErrClientSecretNotFound = errors.New("client secret not found")
)
