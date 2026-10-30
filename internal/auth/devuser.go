package auth

import (
	"net/http"
	"strings"
)

// HeaderDevUser is accepted only by DevUserAuthenticator for local BFF runs.
const HeaderDevUser = "X-Yesbook-User"

// DevUserAuthenticator resolves identity for local/same-machine BFF testing.
// Prefer the X-Yesbook-User header when present, otherwise FixedEmail.
// Do not enable this mode on a publicly reachable Cloud Run service.
type DevUserAuthenticator struct {
	FixedEmail string
}

// Authenticate implements UserAuthenticator.
func (a DevUserAuthenticator) Authenticate(r *http.Request) (string, error) {
	if v := strings.TrimSpace(r.Header.Get(HeaderDevUser)); v != "" {
		return strings.ToLower(v), nil
	}
	if email := strings.ToLower(strings.TrimSpace(a.FixedEmail)); email != "" {
		return email, nil
	}
	return "", ErrUnauthenticated
}
