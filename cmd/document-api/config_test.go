package main

import "testing"

func TestLoadConfigRequiresEveryVariable(t *testing.T) {
	t.Setenv("YESBOOK_DATABASE_URL", "")
	t.Setenv("YESBOOK_API_KEYS", "")
	t.Setenv("YESBOOK_WORKFLOW", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing variables should fail")
	}
	t.Setenv("YESBOOK_DATABASE_URL", "postgres://localhost/yesbook")
	t.Setenv("YESBOOK_API_KEYS", "tenant:0123456789abcdef")
	t.Setenv("YESBOOK_WORKFLOW", "projects/p/locations/r/workflows/w")
	cfg, err := loadConfig()
	if err != nil || cfg.Workflow == "" {
		t.Fatal(err)
	}
}
