package main

import "testing"

func TestLoadConfigRequiresEveryVariable(t *testing.T) {
	t.Setenv("YESBOOK_DATABASE_URL", "")
	t.Setenv("YESBOOK_PROJECT", "")
	t.Setenv("YESBOOK_TOPIC", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing variables should fail")
	}
	t.Setenv("YESBOOK_DATABASE_URL", "postgres://localhost/yesbook")
	t.Setenv("YESBOOK_PROJECT", "yesbook")
	t.Setenv("YESBOOK_TOPIC", "document-events")
	if _, err := loadConfig(); err != nil {
		t.Fatal(err)
	}
}
