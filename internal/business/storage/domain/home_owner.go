package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrInvalidHomeOwner means a HOME owner is empty or not text.
var ErrInvalidHomeOwner = errors.New("storage: invalid HOME owner")

// HomeOwner names a HOME by the owner its caller chose; which owner a Work's
// HOME has is the caller's decision, and Storage never interprets it.
type HomeOwner struct {
	owner string
}

// ParseHomeOwner admits a caller's HOME owner.
func ParseHomeOwner(owner string) (HomeOwner, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || strings.ContainsRune(owner, 0) {
		return HomeOwner{}, ErrInvalidHomeOwner
	}
	return HomeOwner{owner: owner}, nil
}

// Key is the stable name of the owner's HOME: the same owner always reaches
// the same HOME, and different owners never share one.
func (o HomeOwner) Key() string {
	digest := sha256.Sum256([]byte(o.owner))
	return hex.EncodeToString(digest[:])
}
