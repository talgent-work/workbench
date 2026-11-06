package redis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/fx/fxtest"
)

// Config JSON unmarshalling must accept both native numeric DB values and the
// string values produced by the service config loader's env interpolation.
func TestConfigUnmarshalJSON_DBValues(t *testing.T) {
	tests := []struct {
		name string
		data string
		want Config
	}{
		{
			name: "numeric db",
			data: `{"addr":"127.0.0.1:6379","password":"secret","db":2}`,
			want: Config{Addr: "127.0.0.1:6379", Password: "secret", DB: 2},
		},
		{
			name: "string db",
			data: `{"addr":"127.0.0.1:6379","password":"secret","db":"3"}`,
			want: Config{Addr: "127.0.0.1:6379", Password: "secret", DB: 3},
		},
		{
			name: "missing db",
			data: `{"addr":"127.0.0.1:6379","password":"secret"}`,
			want: Config{Addr: "127.0.0.1:6379", Password: "secret"},
		},
		{
			name: "null db",
			data: `{"addr":"127.0.0.1:6379","password":"secret","db":null}`,
			want: Config{Addr: "127.0.0.1:6379", Password: "secret"},
		},
		{
			name: "empty string db",
			data: `{"addr":"127.0.0.1:6379","password":"secret","db":""}`,
			want: Config{Addr: "127.0.0.1:6379", Password: "secret"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Config
			if err := json.Unmarshal([]byte(tt.data), &got); err != nil {
				t.Fatalf("Unmarshal error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Config = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Invalid DB values should fail during config unmarshalling rather than
// silently falling back to DB 0.
func TestConfigUnmarshalJSON_InvalidDBValues(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "invalid string db", data: `{"db":"not-a-number"}`},
		{name: "float db", data: `{"db":1.5}`},
		{name: "object db", data: `{"db":{"value":1}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := json.Unmarshal([]byte(tt.data), &cfg); err == nil {
				t.Fatal("Unmarshal error = nil, want error")
			}
		})
	}
}

// NewClient should preserve Redis connection settings so service config changes
// are reflected in the constructed client.
func TestNewClientUsesConfig(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	client := NewClient(lc, Config{
		Addr:     "127.0.0.1:6380",
		Password: "secret",
		DB:       2,
	})
	t.Cleanup(func() { _ = client.Close() })

	opt := client.Options()
	if opt.Addr != "127.0.0.1:6380" {
		t.Fatalf("Addr = %q, want %q", opt.Addr, "127.0.0.1:6380")
	}
	if opt.Password != "secret" {
		t.Fatalf("Password = %q, want %q", opt.Password, "secret")
	}
	if opt.DB != 2 {
		t.Fatalf("DB = %d, want %d", opt.DB, 2)
	}
}

// NewClient should bind Redis cleanup to the fx lifecycle so dispatcher shutdown
// releases the shared client.
func TestNewClientClosesOnStop(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	client := NewClient(lc, Config{Addr: "127.0.0.1:6380"})

	lc.RequireStart()
	lc.RequireStop()

	err := client.Ping(context.Background()).Err()
	if !errors.Is(err, goredis.ErrClosed) {
		t.Fatalf("Ping after lifecycle stop error = %v, want %v", err, goredis.ErrClosed)
	}
}
