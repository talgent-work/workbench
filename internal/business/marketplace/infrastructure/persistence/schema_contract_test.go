package persistence

import (
	"os"
	"strings"
	"testing"
)

func readInitSchema(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("../../../../../deploy/middleware/init/mysql/001_workbench.sql")
	if err != nil {
		t.Fatalf("read init schema: %v", err)
	}
	return string(body)
}

func scmInitSchema(sql string) string {
	var out strings.Builder
	for _, statement := range strings.Split(sql, ";") {
		if strings.Contains(statement, "CREATE TABLE IF NOT EXISTS `scm_") {
			out.WriteString(statement)
			out.WriteString(";")
		}
	}
	return out.String()
}

// The init schema must include durable SCM refresh/reconnect columns and lookup indexes.
func TestInitSchemaDefinesSCMTokenLifecycleStateAndLookupIndexes(t *testing.T) {
	sql := readInitSchema(t)
	for _, want := range []string{
		"`refresh_token_expires_at` bigint NOT NULL DEFAULT '0'",
		"`status` varchar(32) NOT NULL DEFAULT 'active'",
		"`refresh_lease_id` varchar(64) NOT NULL DEFAULT ''",
		"`refresh_lease_until` bigint NOT NULL DEFAULT '0'",
		"`last_refreshed_at` bigint NOT NULL DEFAULT '0'",
		"`last_checked_at` bigint NOT NULL DEFAULT '0'",
		"`last_failure_code` varchar(128) NOT NULL DEFAULT ''",
		"`last_failure_detail` varchar(512) NOT NULL DEFAULT ''",
		"KEY `idx_refresh_candidates` (`status`, `expires_at`, `refresh_lease_until`, `deleted_at`)",
		"KEY `idx_provider_account` (`provider`, `provider_account_id`, `deleted_at`)",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("init schema missing %q", want)
		}
	}
}
