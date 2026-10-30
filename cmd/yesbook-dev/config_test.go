package main

import "testing"

func TestLoadConfigUsesLocalDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("YESBOOK_API_KEYS", "")
	t.Setenv("YESBOOK_DATABASE_URL", "")
	t.Setenv("YESBOOK_DATA_DIR", "")
	t.Setenv("YESBOOK_LOCAL_SIGNING_KEY", "")
	cfg := loadConfig()
	if cfg.Port != "8080" || cfg.APIKeys != defaultAPIKeys || cfg.DataDir != "data" || len(cfg.SigningKey) < 32 {
		t.Fatalf("config = %+v", cfg)
	}
}
