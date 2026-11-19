// Package credentialcipher encrypts the credentials Talgent holds for an
// owner at rest, for every Provider.
package credentialcipher

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

const tokenCipherVersion = "v1:"

type aesGCMTokenCipher struct {
	aead cipher.AEAD
}

// New is the cipher of the configured key. Without a key it is disabled and
// refuses every operation; a deployment declaring a Provider needs a key.
func New(keyText string) (application.CredentialCipher, error) {
	keyText = strings.TrimSpace(keyText)
	if keyText == "" {
		return disabledTokenCipher{}, nil
	}
	key := DeriveKey(keyText)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &aesGCMTokenCipher{aead: aead}, nil
}

type disabledTokenCipher struct{}

func (disabledTokenCipher) Encrypt(string) (string, error) {
	return "", applicationDisabledError()
}

func (disabledTokenCipher) Decrypt(string) (string, error) {
	return "", applicationDisabledError()
}

func applicationDisabledError() error {
	return fmt.Errorf("credential cipher has no key")
}

func (c *aesGCMTokenCipher) Encrypt(plaintext string) (string, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", fmt.Errorf("token plaintext is required")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return tokenCipherVersion + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *aesGCMTokenCipher) Decrypt(ciphertext string) (string, error) {
	ciphertext = strings.TrimSpace(ciphertext)
	if !strings.HasPrefix(ciphertext, tokenCipherVersion) {
		return "", fmt.Errorf("token ciphertext version is unsupported")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ciphertext, tokenCipherVersion))
	if err != nil {
		return "", err
	}
	nonceSize := c.aead.NonceSize()
	if len(payload) <= nonceSize {
		return "", fmt.Errorf("token ciphertext is invalid")
	}
	plaintext, err := c.aead.Open(nil, payload[:nonceSize], payload[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// DeriveKey is the 32-byte key of the configured text: its base64 decoding
// when that is 32 bytes, otherwise its SHA-256.
func DeriveKey(keyText string) []byte {
	if decoded, err := base64.StdEncoding.DecodeString(keyText); err == nil && len(decoded) == 32 {
		return decoded
	}
	sum := sha256.Sum256([]byte(keyText))
	return sum[:]
}
