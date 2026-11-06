package main

import (
	"testing"

	"go.uber.org/fx"
)

// Every Workbench core context composes into one application:
// each keeps its own providers, so none collides with another's.
func TestControlplaneComposes(t *testing.T) {
	if err := fx.ValidateApp(appOptions(Option{})...); err != nil {
		t.Fatal(err)
	}
}
