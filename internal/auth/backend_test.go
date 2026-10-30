package auth_test

import (
	"errors"
	"testing"

	"github.com/avi-p/yesbook/internal/auth"
)

func TestParseBackendCredentials(t *testing.T) {
	c, err := auth.ParseBackendCredentials("tenant-a:aaaaaaaaaaaaaaaa,tenant-b:bbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := c.BearerToken("tenant-a")
	if err != nil || tok != "Bearer aaaaaaaaaaaaaaaa" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if _, err := c.BearerToken("missing"); !errors.Is(err, auth.ErrUnknownTenant) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseBackendCredentialsRejectsDuplicates(t *testing.T) {
	if _, err := auth.ParseBackendCredentials("t:0123456789abcdef,t:0123456789abcdef"); err == nil {
		t.Fatal("want duplicate error")
	}
}
