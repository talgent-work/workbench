package taskqueue

import "testing"

func TestOptionValidateRequiresRedisAddr(t *testing.T) {
	// Intent (real): taskqueue runtime owns its Redis endpoint requirement; each
	// service main should not duplicate taskqueue leaf-field checks.
	if err := (Option{}).Validate(); err == nil {
		t.Fatal("Validate() err = nil, want redis-addr error")
	}
}
