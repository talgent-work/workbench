package execution

import (
	"crypto/sha256"
	"encoding/hex"
)

func storageKey(reference string) string {
	digest := sha256.Sum256([]byte(reference))
	return hex.EncodeToString(digest[:])
}
