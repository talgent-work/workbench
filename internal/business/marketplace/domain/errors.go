package domain

import "errors"

var (
	ErrInvalidRequiredField = errors.New("marketplace: invalid required field")
	ErrProviderNotFound     = errors.New("marketplace: provider authorization not found")
	ErrRepositoryNotFound   = errors.New("marketplace: repository is not reachable")
	ErrPermissionDenied     = errors.New("marketplace: permission denied")
	ErrInvalidTransition    = errors.New("marketplace: invalid transition")
	// ErrConcurrentModification refuses a change read against a stale version
	// of any Marketplace aggregate: an installation, a Connector or a Provider
	// authorization.
	ErrConcurrentModification = errors.New("marketplace: changed concurrently; reload and try again")
)
