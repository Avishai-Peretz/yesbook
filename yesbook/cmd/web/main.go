// Command web is the same-origin Cloud Run front door: it serves the built
// React SPA and proxies /api to document-api and content-api while injecting
// per-tenant backend credentials. End-user identity comes from IAP (or a
// local-only dev authenticator); see docs/production-web.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/avi-p/yesbook/internal/auth"
	"github.com/avi-p/yesbook/internal/platform"
	"github.com/avi-p/yesbook/internal/web"
)

type config struct {
	DocumentAPI string
	ContentAPI  string
	WebRoot     string
	APIKeys     string
	UserTenants string
	AuthMode    string
	DevUser     string
	RequireJWT  bool
}

func loadConfig() (config, error) {
	var req platform.RequireEnv
	cfg := config{
		DocumentAPI: req.Get("YESBOOK_DOCUMENT_API_URL"),
		ContentAPI:  req.Get("YESBOOK_CONTENT_API_URL"),
		WebRoot:     platform.Env("YESBOOK_WEB_ROOT", "web/dist"),
		APIKeys:     req.Get("YESBOOK_API_KEYS"),
		UserTenants: req.Get("YESBOOK_USER_TENANTS"),
		AuthMode:    strings.ToLower(platform.Env("YESBOOK_AUTH_MODE", "iap")),
		DevUser:     platform.Env("YESBOOK_DEV_USER", ""),
		RequireJWT:  platform.Env("YESBOOK_IAP_REQUIRE_JWT", "") == "1",
	}
	if err := req.Err(); err != nil {
		return config{}, err
	}
	switch cfg.AuthMode {
	case "iap", "dev":
	default:
		return config{}, fmt.Errorf("YESBOOK_AUTH_MODE must be iap or dev, got %q", cfg.AuthMode)
	}
	if cfg.AuthMode == "dev" && cfg.DevUser == "" {
		return config{}, fmt.Errorf("YESBOOK_DEV_USER is required when YESBOOK_AUTH_MODE=dev")
	}
	return cfg, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	log := platform.NewLogger("web")
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	creds, err := auth.ParseBackendCredentials(cfg.APIKeys)
	if err != nil {
		return err
	}
	tenants, err := auth.ParseTenantMap(cfg.UserTenants)
	if err != nil {
		return err
	}
	users, err := newUserAuthenticator(cfg)
	if err != nil {
		return err
	}
	h, err := web.New(web.Config{
		DocumentAPI: cfg.DocumentAPI,
		ContentAPI:  cfg.ContentAPI,
		WebRoot:     cfg.WebRoot,
		Users:       users,
		Tenants:     tenants,
		Credentials: creds,
		Log:         log,
	})
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "web bff ready", "auth_mode", cfg.AuthMode, "web_root", cfg.WebRoot)
	return platform.Serve(ctx, h, log)
}

func newUserAuthenticator(cfg config) (auth.UserAuthenticator, error) {
	switch cfg.AuthMode {
	case "dev":
		logWarnDev()
		return auth.DevUserAuthenticator{FixedEmail: cfg.DevUser}, nil
	default:
		return auth.IAPAuthenticator{RequireJWT: cfg.RequireJWT}, nil
	}
}

func logWarnDev() {
	// Intentionally stderr: make misuse of dev auth visible in Cloud Logging.
	fmt.Fprintln(os.Stderr, "warning: YESBOOK_AUTH_MODE=dev trusts X-Yesbook-User / YESBOOK_DEV_USER; do not expose publicly")
}
