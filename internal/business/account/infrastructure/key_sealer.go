package infrastructure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// keySealer encrypts signing private keys at rest with AES-256-GCM under the
// Account master key, so the database alone never yields a signing key.
type keySealer struct {
	aead cipher.AEAD
}

func newKeySealer(masterKey []byte) (keySealer, error) {
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return keySealer{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return keySealer{}, err
	}
	return keySealer{aead: aead}, nil
}

// seal binds the ciphertext to its kid so a sealed key cannot be swapped
// between rows.
func (k keySealer) seal(plaintext []byte, kid string) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return k.aead.Seal(nonce, nonce, plaintext, []byte(kid)), nil
}

func (k keySealer) open(sealed []byte, kid string) ([]byte, error) {
	size := k.aead.NonceSize()
	if len(sealed) < size {
		return nil, errors.New("sealed signing key is truncated")
	}
	return k.aead.Open(nil, sealed[:size], sealed[size:], []byte(kid))
}
