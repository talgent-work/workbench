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
)
