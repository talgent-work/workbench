package nativeauth

import (
	"net/http"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/pkg/authn"
)

type Config struct {
	// AgentAddress and AgentControlToken reach the Agent that verifies Work
	// credentials.
	AgentAddress      string `json:"agent-address" yaml:"agent-address" toml:"agent-address"`
	AgentControlToken string `json:"agent-control-token" yaml:"agent-control-token" toml:"agent-control-token"`
}

// Module provides the live-Work verifier for reads and, as the
// authn.NativeProcessVerifier, the acting-Work verifier for actions.
var Module = fx.Module("native-authentication", fx.Provide(
	func(client *http.Client, cfg Config) (*LiveWorkClient, error) {
		return NewLiveWorkClient(client, cfg.AgentAddress, cfg.AgentControlToken)
	},
	func(live *LiveWorkClient) authn.NativeProcessVerifier { return NewActingWorkClient(live) },
))
