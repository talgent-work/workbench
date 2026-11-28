// Package sealingtest supplies tests with a Personal Variable sealer under a
// fixed, test-only key.
package sealingtest

import (
	"bytes"

	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/sealing"
)

// Key is the test-only Agent key.
var Key = bytes.Repeat([]byte{0x5a}, sealing.KeySize)

// Sealer seals under Key.
func Sealer() *sealing.Sealer {
	sealer, err := sealing.NewSealer(Key)
	if err != nil {
		panic(err)
	}
	return sealer
}
