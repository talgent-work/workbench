package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// opaqueSecretBytes is the entropy of every secret Account hands out once
// and keeps only as a digest: refresh secrets and Client Secrets.
const opaqueSecretBytes = 32

func newOpaqueSecret() (string, error) {
	raw := make([]byte, opaqueSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// isOpaqueSecret accepts only the issued form.
func isOpaqueSecret(raw string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	return err == nil && len(decoded) == opaqueSecretBytes && !strings.ContainsAny(raw, " \t\n")
}

func opaqueDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func validOpaqueDigest(digest string) bool {
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size
}
