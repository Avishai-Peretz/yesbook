package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/httpapi"
)

// Config wires the same-origin web BFF.
type Config struct {
	DocumentAPI string
	ContentAPI  string
	WebRoot     string
	Users       auth.UserAuthenticator
	Tenants     auth.TenantMapper
	Credentials *auth.BackendCredentials
	Log         *slog.Logger
}

// New returns the production web handler: /healthz and /api/healthz for probes,
// /api/* proxied to the backend APIs with injected tenant credentials, and the
// SPA for everything else.
func New(cfg Config) (http.Handler, error) {
	if cfg.Users == nil || cfg.Tenants == nil || cfg.Credentials == nil {
		return nil, fmt.Errorf("web: users, tenants, and credentials are required")
	}
	if cfg.Log == nil {
		return nil, fmt.Errorf("web: logger is required")
	}
	docURL, err := parseOrigin(cfg.DocumentAPI, "document API")
	if err != nil {
		return nil, err
	}
	contURL, err := parseOrigin(cfg.ContentAPI, "content API")
	if err != nil {
		return nil, err
	}
	spa, err := NewSPA(cfg.WebRoot)
	if err != nil {
		return nil, fmt.Errorf("web root: %w", err)
	}

	p := newProxy(docURL, contURL, cfg.Users, cfg.Tenants, cfg.Credentials)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle(APIPrefix+"/", p)
	mux.Handle("/", spa)
	return httpapi.Middleware(mux, cfg.Log), nil
}

func parseOrigin(raw, name string) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("web: %s URL is required", name)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("web: invalid %s URL %q", name, raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}
