// Package auth resolves the tenant of a request from its API key.
package auth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrUnauthenticated = errors.New("missing or invalid API key")

// APIKeys maps SHA-256 hashes of keys to tenants, so raw keys are never kept
// in memory after parsing and a lookup does not compare secrets byte by byte.
type APIKeys struct {
	tenants map[[sha256.Size]byte]string
}

// ParseAPIKeys reads "tenant:key" pairs separated by commas, as stored in
// Secret Manager.
func ParseAPIKeys(spec string) (*APIKeys, error) {
	k := &APIKeys{tenants: make(map[[sha256.Size]byte]string)}
	for pair := range strings.SplitSeq(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		tenant, key, ok := strings.Cut(pair, ":")
		if !ok || tenant == "" || len(key) < 16 {
			return nil, fmt.Errorf("invalid API key entry for tenant %q: want tenant:key with a key of at least 16 characters", tenant)
		}
		k.tenants[sha256.Sum256([]byte(key))] = tenant
	}
	if len(k.tenants) == 0 {
		return nil, errors.New("no API keys configured")
	}
	return k, nil
}

// Authenticate accepts "Authorization: Bearer <key>" or "X-Api-Key: <key>".
func (k *APIKeys) Authenticate(r *http.Request) (string, error) {
	key := r.Header.Get("X-Api-Key")
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		key = bearer
	}
	if key == "" {
		return "", ErrUnauthenticated
	}
	tenant, ok := k.tenants[sha256.Sum256([]byte(key))]
	if !ok {
		return "", ErrUnauthenticated
	}
	return tenant, nil
}
