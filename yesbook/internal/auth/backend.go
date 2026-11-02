package auth

import (
	"fmt"
	"strings"
)

// BackendCredentials holds the per-tenant API keys the web BFF injects when
// calling document-api and content-api. Raw keys stay in process memory only
// on the server side; they are never shipped to the browser.
type BackendCredentials struct {
	byTenant map[string]string
}

// ParseBackendCredentials reads the same "tenant:key" comma-separated form as
// ParseAPIKeys (and YESBOOK_API_KEYS in Secret Manager).
func ParseBackendCredentials(spec string) (*BackendCredentials, error) {
	c := &BackendCredentials{byTenant: make(map[string]string)}
	for pair := range strings.SplitSeq(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		tenant, key, ok := strings.Cut(pair, ":")
		if !ok || tenant == "" || len(key) < 16 {
			return nil, fmt.Errorf("invalid API key entry for tenant %q: want tenant:key with a key of at least 16 characters", tenant)
		}
		if _, exists := c.byTenant[tenant]; exists {
			return nil, fmt.Errorf("duplicate API key entry for tenant %q", tenant)
		}
		c.byTenant[tenant] = key
	}
	if len(c.byTenant) == 0 {
		return nil, fmt.Errorf("no API keys configured")
	}
	return c, nil
}

// BearerToken returns the Authorization header value for tenant, or
// ErrUnknownTenant when the tenant has no configured key.
func (c *BackendCredentials) BearerToken(tenant string) (string, error) {
	if c == nil {
		return "", ErrUnknownTenant
	}
	key, ok := c.byTenant[tenant]
	if !ok {
		return "", ErrUnknownTenant
	}
	return "Bearer " + key, nil
}

// HasTenant reports whether a backend key is configured for tenant.
func (c *BackendCredentials) HasTenant(tenant string) bool {
	if c == nil {
		return false
	}
	_, ok := c.byTenant[tenant]
	return ok
}
