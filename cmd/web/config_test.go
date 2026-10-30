package main

import "testing"

func TestLoadConfigRequiresVariables(t *testing.T) {
	t.Setenv("YESBOOK_DOCUMENT_API_URL", "")
	t.Setenv("YESBOOK_CONTENT_API_URL", "")
	t.Setenv("YESBOOK_API_KEYS", "")
	t.Setenv("YESBOOK_USER_TENANTS", "")
	t.Setenv("YESBOOK_AUTH_MODE", "iap")
	t.Setenv("YESBOOK_DEV_USER", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing variables should fail")
	}

	t.Setenv("YESBOOK_DOCUMENT_API_URL", "http://127.0.0.1:8081")
	t.Setenv("YESBOOK_CONTENT_API_URL", "http://127.0.0.1:8082")
	t.Setenv("YESBOOK_API_KEYS", "tenant-a:0123456789abcdef")
	t.Setenv("YESBOOK_USER_TENANTS", "user@example.com:tenant-a")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AuthMode != "iap" || cfg.WebRoot == "" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadConfigDevRequiresUser(t *testing.T) {
	t.Setenv("YESBOOK_DOCUMENT_API_URL", "http://127.0.0.1:8081")
	t.Setenv("YESBOOK_CONTENT_API_URL", "http://127.0.0.1:8082")
	t.Setenv("YESBOOK_API_KEYS", "tenant-a:0123456789abcdef")
	t.Setenv("YESBOOK_USER_TENANTS", "dev@localhost:tenant-a")
	t.Setenv("YESBOOK_AUTH_MODE", "dev")
	t.Setenv("YESBOOK_DEV_USER", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("dev mode without YESBOOK_DEV_USER should fail")
	}
	t.Setenv("YESBOOK_DEV_USER", "dev@localhost")
	if _, err := loadConfig(); err != nil {
		t.Fatal(err)
	}
}
