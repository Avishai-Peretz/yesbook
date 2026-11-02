package auth

import (
	"net/http"
	"strings"
)

// IAP header names set by Google Identity-Aware Proxy when it fronts Cloud Run
// or HTTPS Load Balancing. The JWT assertion can be verified separately; this
// authenticator trusts the email header only when IAP is enforced on the
// ingress path (see docs/production-web.md).
const (
	HeaderIAPEmail = "X-Goog-Authenticated-User-Email"
	HeaderIAPJWT   = "X-Goog-IAP-JWT-Assertion"
)

// IAPAuthenticator reads the authenticated user email from IAP request
// headers. It does not verify the JWT itself; Cloud Run / IAP must strip
// forged headers from the public internet. Set RequireJWT to also demand that
// the assertion header is present (without cryptographic verification unless
// a Verifier is plugged in).
type IAPAuthenticator struct {
	// RequireJWT, when true, rejects requests that lack X-Goog-IAP-JWT-Assertion.
	RequireJWT bool
	// Verifier optionally validates the IAP JWT. When nil, only headers are checked.
	Verifier IAPJWTVerifier
}

// IAPJWTVerifier validates an IAP JWT assertion for the configured audience.
// Production deployments should wire a real verifier (audience from the IAP
// OAuth client / backend service); leave nil for header-only trust behind IAP.
type IAPJWTVerifier interface {
	Verify(assertion string) (email string, err error)
}

// Authenticate implements UserAuthenticator.
func (a IAPAuthenticator) Authenticate(r *http.Request) (string, error) {
	assertion := r.Header.Get(HeaderIAPJWT)
	if a.Verifier != nil {
		if assertion == "" {
			return "", ErrUnauthenticated
		}
		email, err := a.Verifier.Verify(assertion)
		if err != nil {
			return "", ErrUnauthenticated
		}
		return normalizeIAPEmail(email), nil
	}
	if a.RequireJWT && assertion == "" {
		return "", ErrUnauthenticated
	}
	email := normalizeIAPEmail(r.Header.Get(HeaderIAPEmail))
	if email == "" {
		return "", ErrUnauthenticated
	}
	return email, nil
}

func normalizeIAPEmail(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "accounts.google.com:")
	return strings.ToLower(strings.TrimSpace(raw))
}
