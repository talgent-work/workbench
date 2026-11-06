package application

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/business/account/domain"
)

const (
	// minSearchTerm is the shortest username fragment the directory searches,
	// as the User Directory Query Contract states.
	minSearchTerm         = 2
	defaultDirectoryLimit = 20
	maxDirectoryLimit     = 50
	maxDirectoryIDs       = 500
)

// ErrDirectoryQueryInvalid rejects a directory read outside its bounds.
var ErrDirectoryQueryInvalid = errors.New("invalid user directory query")

// SearchUsers finds Accounts whose username contains the term, ignoring case.
// The directory is platform-wide; callers apply their own scope.
func (a *Application) SearchUsers(ctx context.Context, term string, limit int) ([]query.User, error) {
	term = strings.TrimSpace(term)
	if utf8.RuneCountInString(term) < minSearchTerm {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultDirectoryLimit
	}
	return a.directory.SearchByUsername(ctx, term, min(limit, maxDirectoryLimit))
}

// GetUsers reads Accounts by id; ids that are not Accounts are omitted.
func (a *Application) GetUsers(ctx context.Context, ids []string) ([]query.User, error) {
	if len(ids) > maxDirectoryIDs {
		return nil, ErrDirectoryQueryInvalid
	}
	seen := make(map[string]struct{}, len(ids))
	valid := make([]string, 0, len(ids))
	for _, raw := range ids {
		id, err := domain.ParseAccountID(raw)
		if err != nil {
			continue
		}
		if _, ok := seen[string(id)]; ok {
			continue
		}
		seen[string(id)] = struct{}{}
		valid = append(valid, string(id))
	}
	if len(valid) == 0 {
		return nil, nil
	}
	return a.directory.GetByIDs(ctx, valid)
}
