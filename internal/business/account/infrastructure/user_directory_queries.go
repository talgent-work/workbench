package infrastructure

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/samber/oops"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/account/application/query"
)

// UserDirectoryQueries reads the platform-wide User Directory from Accounts.
type UserDirectoryQueries struct {
	engine *xorm.Engine
}

var _ query.UserDirectoryQueryRepository = (*UserDirectoryQueries)(nil)

func NewUserDirectoryQueries(engine *xorm.Engine) *UserDirectoryQueries {
	return &UserDirectoryQueries{engine: engine}
}

// SearchByUsername finds usernames containing the term. The ngram FULLTEXT
// index covers username_search, the username without hyphens plus a sentinel
// character, in which every character of the login starts a bigram. The term
// without hyphens is required there, as a phrase or, for one character, as a
// token prefix: a login containing the term contains that string too, so the
// index never drops a match. LIKE on the username then confirms exact,
// case-insensitive containment among the bounded candidates.
func (r *UserDirectoryQueries) SearchByUsername(ctx context.Context, term string, limit int) ([]query.User, error) {
	terms, ok := indexedTerm(term)
	if !ok {
		return nil, nil
	}
	var rows []accountDO
	err := r.engine.Context(ctx).
		Cols("id", "username", "email", "display_name", "avatar_url").
		Where("deleted_at = 0 AND MATCH(username_search) AGAINST(? IN BOOLEAN MODE) AND username LIKE ? ESCAPE '!'",
			terms, "%"+escapeLike(term)+"%").
		OrderBy("username, id").
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, oops.With("operation", "user_directory.search").Wrap(err)
	}
	return directoryUsers(rows), nil
}

// GitHub logins hold only letters, digits and hyphens; a term with anything
// else is contained in no username.
var loginFragment = regexp.MustCompile(`^[\p{L}\p{N}-]+$`)

func indexedTerm(term string) (string, bool) {
	if !loginFragment.MatchString(term) {
		return "", false
	}
	stripped := strings.ReplaceAll(term, "-", "")
	switch utf8.RuneCountInString(stripped) {
	case 0:
		return "", false
	case 1:
		return "+" + stripped + "*", true
	default:
		return `+"` + stripped + `"`, true
	}
}

// escapeLike makes the term match literally; '!' is the declared escape.
func escapeLike(term string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(term)
}

// GetByIDs reads in chunks of at most 50 ids per IN list.
func (r *UserDirectoryQueries) GetByIDs(ctx context.Context, ids []string) ([]query.User, error) {
	out := make([]query.User, 0, len(ids))
	for start := 0; start < len(ids); start += 50 {
		chunk := ids[start:min(start+50, len(ids))]
		var rows []accountDO
		err := r.engine.Context(ctx).
			Cols("id", "username", "email", "display_name", "avatar_url").
			Where("deleted_at = 0").
			In("id", chunk).
			OrderBy("id").
			Find(&rows)
		if err != nil {
			return nil, oops.With("operation", "user_directory.get").Wrap(err)
		}
		out = append(out, directoryUsers(rows)...)
	}
	return out, nil
}

func directoryUsers(rows []accountDO) []query.User {
	out := make([]query.User, 0, len(rows))
	for i := range rows {
		out = append(out, directoryUserFromDO(&rows[i]))
	}
	return out
}
