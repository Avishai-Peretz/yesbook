package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/avi-p/yesbook/internal/auth"
)

// APIPrefix is the browser-facing path prefix the SPA uses for backend calls.
const APIPrefix = "/api"

type ctxKey int

const backendAuthKey ctxKey = 1

type proxy struct {
	document http.Handler
	content  http.Handler
	users    auth.UserAuthenticator
	tenants  auth.TenantMapper
	creds    *auth.BackendCredentials
}

func newProxy(documentURL, contentURL *url.URL, users auth.UserAuthenticator, tenants auth.TenantMapper, creds *auth.BackendCredentials) *proxy {
	return &proxy{
		document: reverseProxy(documentURL),
		content:  reverseProxy(contentURL),
		users:    users,
		tenants:  tenants,
		creds:    creds,
	}
}

func reverseProxy(target *url.URL) http.Handler {
	p := httputil.NewSingleHostReverseProxy(target)
	original := p.Director
	p.Director = func(r *http.Request) {
		original(r)
		r.Host = target.Host
		// Drop client-supplied auth/identity; reinject the tenant backend key.
		r.Header.Del("Authorization")
		r.Header.Del("X-Api-Key")
		r.Header.Del(auth.HeaderIAPEmail)
		r.Header.Del(auth.HeaderIAPJWT)
		r.Header.Del(auth.HeaderDevUser)
		if tok, ok := r.Context().Value(backendAuthKey).(string); ok && tok != "" {
			r.Header.Set("Authorization", tok)
		}
	}
	p.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}
	return p
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, APIPrefix)
	if path == "" {
		path = "/"
	}
	if path == "/healthz" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	tenant, err := auth.ResolveTenant(r, p.users, p.tenants, p.creds)
	if err != nil {
		status, msg := authStatus(err)
		writeJSONError(w, status, msg)
		return
	}
	token, err := p.creds.BearerToken(tenant)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, "forbidden")
		return
	}

	ctx := context.WithValue(r.Context(), backendAuthKey, token)
	r = r.Clone(ctx)
	r.URL.Path = path
	r.URL.RawPath = ""
	r.RequestURI = ""
	r.Header.Set("X-Yesbook-Tenant", tenant)

	upstream := p.document
	if isContentPath(path) {
		upstream = p.content
	}
	upstream.ServeHTTP(w, r)
}

func isContentPath(path string) bool {
	if strings.HasPrefix(path, "/v1/versions/") {
		return true
	}
	trimmed := strings.TrimSuffix(path, "/")
	return strings.HasPrefix(trimmed, "/v1/documents/") && strings.HasSuffix(trimmed, "/content")
}

func authStatus(err error) (int, string) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return http.StatusUnauthorized, "missing or invalid credentials"
	case errors.Is(err, auth.ErrUnknownTenant), errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	default:
		return http.StatusUnauthorized, "missing or invalid credentials"
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
