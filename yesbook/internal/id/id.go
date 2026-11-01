// Package id generates random identifiers.
package id

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns a random 128-bit identifier encoded as 32 hex characters.
func New() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
