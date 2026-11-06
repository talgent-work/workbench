package application

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	diagnosisURL   = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s'"<>]+`)
	diagnosisToken = regexp.MustCompile(`(?i)\bbearer\s+\S+|\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|npm_[A-Za-z0-9]{36})`)
	// diagnosisUserInfo is a user and password before a host outside a URL, as
	// in an scp-like address user:password@host:path.
	diagnosisUserInfo = regexp.MustCompile(`[^\s/@:'"]+:[^\s/@'"]+@`)
)

const diagnosisLimit = 512

// SafeDiagnosis is what a synchronization failure may tell an operator: the
// cause as the source answered it, with every URL reduced to its scheme, host
// and path, a user and password before any other host removed, and GitHub,
// GitLab, npm and bearer tokens replaced, so no credential that appears in
// those forms, query or fragment reaches the run status or the logs.
func SafeDiagnosis(text string) string {
	text = diagnosisURL.ReplaceAllStringFunc(text, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "[redacted URL]"
		}
		return u.Scheme + "://" + u.Host + u.EscapedPath()
	})
	text = diagnosisUserInfo.ReplaceAllString(text, "[redacted]@")
	text = strings.Join(strings.Fields(diagnosisToken.ReplaceAllString(text, "[redacted]")), " ")
	if len(text) > diagnosisLimit {
		text = strings.ToValidUTF8(text[:diagnosisLimit], "") + "…"
	}
	return text
}
