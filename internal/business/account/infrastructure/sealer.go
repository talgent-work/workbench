package infrastructure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// sealer encrypts a secret at rest with AES-256-GCM under a key derived from
// the Account master key, so the database alone never yields it: signing
// private keys and the replacing secret of a Login Session's refresh replay.
type sealer struct {
	aead cipher.AEAD
}

func newSealer(key []byte) (sealer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return sealer{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return sealer{}, err
	}
	return sealer{aead: aead}, nil
}

// seal binds the ciphertext to its owning row's identity, so a sealed value
// cannot be swapped between rows.
func (k sealer) seal(plaintext []byte, owner string) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return k.aead.Seal(nonce, nonce, plaintext, []byte(owner)), nil
}

func (k sealer) open(sealed []byte, owner string) ([]byte, error) {
	size := k.aead.NonceSize()
	if len(sealed) < size {
		return nil, errors.New("sealed value is truncated")
	}
	return k.aead.Open(nil, sealed[:size], sealed[size:], []byte(owner))
}
