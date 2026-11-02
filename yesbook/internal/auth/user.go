package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrUnknownTenant means the authenticated user is not mapped to a tenant, or
// the tenant has no backend credential.
var ErrUnknownTenant = errors.New("unknown tenant")

// ErrForbidden means the caller is authenticated but not allowed.
var ErrForbidden = errors.New("forbidden")

// UserAuthenticator resolves the end-user identity of a browser request.
// Implementations must not invent production secrets; production deployments
// typically trust Identity-Aware Proxy headers or verify the IAP JWT.
type UserAuthenticator interface {
	Authenticate(r *http.Request) (identity string, err error)
}

// TenantMapper maps a user identity (email or subject) to a tenant ID.
type TenantMapper interface {
	Tenant(identity string) (tenantID string, err error)
}

// StaticTenantMap is a fixed identity→tenant table from configuration.
type StaticTenantMap struct {
	byIdentity map[string]string
}

// ParseTenantMap reads "identity:tenant" pairs separated by commas.
// Identities are compared case-insensitively after trimming; Google account
// emails are the usual identity when IAP is enabled.
func ParseTenantMap(spec string) (*StaticTenantMap, error) {
	m := &StaticTenantMap{byIdentity: make(map[string]string)}
	for pair := range strings.SplitSeq(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		identity, tenant, ok := strings.Cut(pair, ":")
		identity = strings.ToLower(strings.TrimSpace(identity))
		tenant = strings.TrimSpace(tenant)
		if !ok || identity == "" || tenant == "" {
			return nil, fmt.Errorf("invalid user-tenant entry %q: want identity:tenant", pair)
		}
		if _, exists := m.byIdentity[identity]; exists {
			return nil, fmt.Errorf("duplicate user-tenant entry for %q", identity)
		}
		m.byIdentity[identity] = tenant
	}
	if len(m.byIdentity) == 0 {
		return nil, errors.New("no user-tenant mappings configured")
	}
	return m, nil
}

// Tenant implements TenantMapper.
func (m *StaticTenantMap) Tenant(identity string) (string, error) {
	if m == nil {
		return "", ErrUnknownTenant
	}
	tenant, ok := m.byIdentity[strings.ToLower(strings.TrimSpace(identity))]
	if !ok {
		return "", ErrUnknownTenant
	}
	return tenant, nil
}

// ResolveTenant authenticates the user and maps them to a tenant that has a
// backend credential. Prefer this at the BFF edge before proxying.
func ResolveTenant(r *http.Request, users UserAuthenticator, tenants TenantMapper, creds *BackendCredentials) (tenantID string, err error) {
	identity, err := users.Authenticate(r)
	if err != nil {
		return "", err
	}
	tenant, err := tenants.Tenant(identity)
	if err != nil {
		return "", err
	}
	if !creds.HasTenant(tenant) {
		return "", ErrUnknownTenant
	}
	return tenant, nil
}
