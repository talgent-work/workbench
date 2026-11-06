package tokenmaintenance

import (
	"encoding/json"
	"testing"
	"time"
)

// Omitted maintenance config should still start the worker with the documented
// defaults so deployments do not silently lose proactive refresh.
func TestConfigNormalize_DefaultsEnabledMaintenance(t *testing.T) {
	cfg, err := (Config{}).Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	enabled, err := cfg.EnabledValue()
	if err != nil {
		t.Fatalf("EnabledValue: %v", err)
	}
	if !enabled {
		t.Fatal("enabled=false, want true")
	}
	if cfg.ScanInterval.Duration() != 5*time.Minute ||
		cfg.ProactiveRefreshWindow.Duration() != 30*time.Minute ||
		cfg.RequestRefreshSkew.Duration() != 5*time.Minute ||
		cfg.RefreshLeaseDuration.Duration() != 30*time.Second ||
		cfg.BatchSize != 100 ||
		cfg.QueueName != "scm-maintenance" {
		t.Fatalf("defaults=%+v", cfg)
	}
}

// The config loader may pass boolean-looking values as JSON booleans or strings;
// both forms must parse so profile overlays and env placeholders behave alike.
func TestBool_UnmarshalJSONAcceptsBoolAndString(t *testing.T) {
	for _, raw := range []string{`false`, `"false"`} {
		var enabled Bool
		if err := json.Unmarshal([]byte(raw), &enabled); err != nil {
			t.Fatalf("UnmarshalJSON(%s): %v", raw, err)
		}
		got, err := enabled.Bool(true)
		if err != nil {
			t.Fatalf("Bool(%s): %v", raw, err)
		}
		if got {
			t.Fatalf("Bool(%s)=true, want false", raw)
		}
	}
}
