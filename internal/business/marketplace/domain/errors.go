package domain

import "errors"

var (
	ErrInvalidRequiredField = errors.New("marketplace: invalid required field")
	ErrProviderNotFound     = errors.New("marketplace: provider authorization not found")
	ErrRepositoryNotFound   = errors.New("marketplace: repository is not reachable")
	ErrPermissionDenied     = errors.New("marketplace: permission denied")
	ErrInvalidTransition    = errors.New("marketplace: invalid transition")
)
