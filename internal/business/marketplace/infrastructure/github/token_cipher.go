package github

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const tokenCipherVersion = "v1:"

type TokenCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

type aesGCMTokenCipher struct {
	aead cipher.AEAD
}

func NewTokenCipher(cfg Config) (TokenCipher, error) {
	enabled, err := cfg.enabled()
	if err != nil {
		return nil, err
	}
	keyText := strings.TrimSpace(cfg.TokenCipherKey)
	if keyText == "" {
		if !enabled {
			return disabledTokenCipher{}, nil
		}
		return nil, fmt.Errorf("github token cipher key is required")
	}
	key := deriveCipherKey(keyText)
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
	return fmt.Errorf("github token cipher is disabled")
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

func deriveCipherKey(keyText string) []byte {
	if decoded, err := base64.StdEncoding.DecodeString(keyText); err == nil && len(decoded) == 32 {
		return decoded
	}
	sum := sha256.Sum256([]byte(keyText))
	return sum[:]
}
