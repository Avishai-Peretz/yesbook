// Package auth resolves the tenant behind an API request.
package auth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrUnauthenticated = errors.New("unauthenticated")

type Authenticator interface {
	Authenticate(r *http.Request) (tenantID string, err error)
}

// StaticKeys authenticates "Authorization: Bearer <key>" against a fixed set
// of API keys. Keys are stored as SHA-256 digests so lookups do not compare
// secrets byte by byte.
type StaticKeys struct {
	tenants map[[sha256.Size]byte]string
}

// ParseStaticKeys parses "key1:tenant1,key2:tenant2".
func ParseStaticKeys(spec string) (*StaticKeys, error) {
	s := &StaticKeys{tenants: make(map[[sha256.Size]byte]string)}
	for pair := range strings.SplitSeq(spec, ",") {
		key, tenant, ok := strings.Cut(strings.TrimSpace(pair), ":")
		if !ok || key == "" || tenant == "" {
			return nil, fmt.Errorf("invalid API key entry %q, want key:tenant", pair)
		}
		s.tenants[sha256.Sum256([]byte(key))] = tenant
	}
	return s, nil
}

func (s *StaticKeys) Authenticate(r *http.Request) (string, error) {
	key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || key == "" {
		return "", ErrUnauthenticated
	}
	tenant, ok := s.tenants[sha256.Sum256([]byte(key))]
	if !ok {
		return "", ErrUnauthenticated
	}
	return tenant, nil
}
