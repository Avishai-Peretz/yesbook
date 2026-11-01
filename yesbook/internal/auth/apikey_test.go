package auth_test

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/avi-p/yesbook/internal/auth"
)

func TestAPIKeys(t *testing.T) {
	keys, err := auth.ParseAPIKeys(" tenant-a:aaaaaaaaaaaaaaaa , tenant-b:bbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, header, value, want string
	}{
		{"bearer", "Authorization", "Bearer aaaaaaaaaaaaaaaa", "tenant-a"},
		{"api key header", "X-Api-Key", "bbbbbbbbbbbbbbbb", "tenant-b"},
		{"unknown key", "X-Api-Key", "cccccccccccccccc", ""},
		{"missing", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tt.header != "" {
				r.Header.Set(tt.header, tt.value)
			}
			got, err := keys.Authenticate(r)
			if tt.want == "" {
				if !errors.Is(err, auth.ErrUnauthenticated) {
					t.Errorf("err = %v, want ErrUnauthenticated", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("tenant = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestParseAPIKeysRejectsShortKeys(t *testing.T) {
	if _, err := auth.ParseAPIKeys("tenant-a:short"); err == nil {
		t.Error("want error for a short key")
	}
	if _, err := auth.ParseAPIKeys(""); err == nil {
		t.Error("want error when no keys are configured")
	}
}
