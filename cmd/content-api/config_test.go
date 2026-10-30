package main

import "testing"

func TestLoadConfigRequiresEveryVariable(t *testing.T) {
	t.Setenv("YESBOOK_DATABASE_URL", "")
	t.Setenv("YESBOOK_API_KEYS", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing variables should fail")
	}
	t.Setenv("YESBOOK_DATABASE_URL", "postgres://localhost/yesbook")
	t.Setenv("YESBOOK_API_KEYS", "tenant:0123456789abcdef")
	if _, err := loadConfig(); err != nil {
		t.Fatal(err)
	}
}
