package config

import (
	"strings"
	"testing"
)

func TestLoadDefaultsInDev(t *testing.T) {
	t.Setenv("ENV", "")
	t.Setenv("PORT", "")
	t.Setenv("SECRET_KEY", "")
	t.Setenv("DB_PATH", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("COOKIE_SECURE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("expected Port 8080, got %q", cfg.Port)
	}
	if cfg.DatabaseURL != defaultDBPath {
		t.Fatalf("expected DatabaseURL %q, got %q", defaultDBPath, cfg.DatabaseURL)
	}
	if cfg.AccessTokenExpireMinutes != 1440 {
		t.Fatalf("expected AccessTokenExpireMinutes 1440, got %d", cfg.AccessTokenExpireMinutes)
	}
	if cfg.MaxUploadBytes != 15*1024*1024 {
		t.Fatalf("expected MaxUploadBytes 15MB, got %d", cfg.MaxUploadBytes)
	}
	if cfg.CookieSecure {
		t.Fatal("expected CookieSecure false in development by default")
	}
	if cfg.SecretKey == "" {
		t.Fatal("expected ephemeral SecretKey in development")
	}
}

func TestLoadProductionRequirements(t *testing.T) {
	t.Setenv("ENV", "production")
	t.Setenv("DB_PATH", "/app/data/syllabuddy.db")
	t.Setenv("SECRET_KEY", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected error when SECRET_KEY is missing in production")
	}

	t.Setenv("SECRET_KEY", "super-secret-prod-key")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() in production unexpected error: %v", err)
	}
	if !cfg.IsProduction() || !cfg.CookieSecure {
		t.Fatal("expected IsProduction=true and CookieSecure=true")
	}
	if cfg.DatabaseURL != "/app/data/syllabuddy.db" {
		t.Fatalf("expected DB_PATH override, got %q", cfg.DatabaseURL)
	}
	if strings.Contains(cfg.String(), "super-secret-prod-key") {
		t.Fatal("String() leaked secret key")
	}
}
