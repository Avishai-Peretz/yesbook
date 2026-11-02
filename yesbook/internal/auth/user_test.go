package auth_test

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/avi-p/yesbook/internal/auth"
)

func TestStaticTenantMap(t *testing.T) {
	m, err := auth.ParseTenantMap("User@Example.com:tenant-a, other@x.com:tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Tenant("user@example.com")
	if err != nil || got != "tenant-a" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := m.Tenant("nobody@x.com"); !errors.Is(err, auth.ErrUnknownTenant) {
		t.Fatalf("err = %v", err)
	}
}

func TestIAPAuthenticator(t *testing.T) {
	a := auth.IAPAuthenticator{}
	r := httptest.NewRequest("GET", "/", nil)
	if _, err := a.Authenticate(r); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("err = %v", err)
	}
	r.Header.Set(auth.HeaderIAPEmail, "accounts.google.com:User@Example.com")
	got, err := a.Authenticate(r)
	if err != nil || got != "user@example.com" {
		t.Fatalf("got %q, %v", got, err)
	}

	strict := auth.IAPAuthenticator{RequireJWT: true}
	if _, err := strict.Authenticate(r); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("require jwt err = %v", err)
	}
	r.Header.Set(auth.HeaderIAPJWT, "assertion")
	got, err = strict.Authenticate(r)
	if err != nil || got != "user@example.com" {
		t.Fatalf("strict got %q, %v", got, err)
	}
}

func TestDevUserAuthenticator(t *testing.T) {
	a := auth.DevUserAuthenticator{FixedEmail: "dev@localhost"}
	r := httptest.NewRequest("GET", "/", nil)
	got, err := a.Authenticate(r)
	if err != nil || got != "dev@localhost" {
		t.Fatalf("got %q, %v", got, err)
	}
	r.Header.Set(auth.HeaderDevUser, "Other@Localhost")
	got, err = a.Authenticate(r)
	if err != nil || got != "other@localhost" {
		t.Fatalf("header got %q, %v", got, err)
	}
}

func TestResolveTenant(t *testing.T) {
	creds, err := auth.ParseBackendCredentials("tenant-a:0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := auth.ParseTenantMap("dev@localhost:tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	users := auth.DevUserAuthenticator{FixedEmail: "dev@localhost"}
	r := httptest.NewRequest("GET", "/", nil)
	tenant, err := auth.ResolveTenant(r, users, tenants, creds)
	if err != nil || tenant != "tenant-a" {
		t.Fatalf("tenant = %q, %v", tenant, err)
	}

	tenantsB, err := auth.ParseTenantMap("dev@localhost:tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ResolveTenant(r, users, tenantsB, creds); !errors.Is(err, auth.ErrUnknownTenant) {
		t.Fatalf("err = %v", err)
	}
}
