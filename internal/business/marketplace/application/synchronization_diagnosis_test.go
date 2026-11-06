package application_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

// A diagnosis keeps what the source said and where, never a credential.
func TestSafeDiagnosisKeepsTheCauseWithoutCredentials(t *testing.T) {
	for _, tc := range []struct{ name, raw, keeps string }{
		{"URL user information and query", "fatal: unable to access 'https://deploy:s3cret@git.example.invalid/catalog.git/?token=s3cret': 403", "https://git.example.invalid/catalog.git/"},
		{"an scp-like address with a password", "fatal: deploy:s3cret@git.example.invalid:catalog.git refused", "[redacted]@git.example.invalid:catalog.git refused"},
		{"a GitHub token", "auth failed for ghp_s3cret0123456789abcdefghijklmnopqrstuv", "auth failed for [redacted]"}, // gitleaks:allow (a fake token the redaction must remove)
		{"a bearer token", "header Authorization: Bearer s3cret rejected", "Authorization: [redacted] rejected"},
		{"an SSH refusal", "git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", "git@github.com: Permission denied (publickey). fatal: Could not read from remote repository."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := application.SafeDiagnosis(tc.raw)
			require.Contains(t, got, tc.keeps)
			require.NotContains(t, strings.ToLower(got), "s3cret")
		})
	}
	require.LessOrEqual(t, len(application.SafeDiagnosis(strings.Repeat("x", 4096))), 520, "a diagnosis is bounded")
}
