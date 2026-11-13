package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort                     = "8080"
	defaultEnv                      = "development"
	defaultDBPath                   = "./data/syllabuddy.db"
	defaultUploadDir                = "./uploads"
	defaultAccessTokenExpireMinutes = 1440
	defaultMaxUploadBytes           = int64(15 * 1024 * 1024)
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	Port                     string
	Env                      string
	DatabaseURL              string
	SecretKey                string
	AccessTokenExpireMinutes int
	BetaInviteCode           string
	GeminiAPIKey             string
	GoogleClientID           string
	UploadDir                string
	CookieSecure             bool
	AppBaseURL               string
	MaxUploadBytes           int64
}

// IsProduction reports whether the application is running in production mode.
func (c *Config) IsProduction() bool {
	return strings.EqualFold(strings.TrimSpace(c.Env), "production")
}

// AccessTokenTTL returns the JWT expiration duration.
func (c *Config) AccessTokenTTL() time.Duration {
	return time.Duration(c.AccessTokenExpireMinutes) * time.Minute
}

// LogValue implements slog.LogValuer so secrets are never emitted in structured logs.
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("port", c.Port),
		slog.String("env", c.Env),
		slog.Bool("database_configured", c.DatabaseURL != ""),
		slog.Bool("secret_key_configured", c.SecretKey != ""),
		slog.Int("access_token_expire_minutes", c.AccessTokenExpireMinutes),
		slog.Bool("beta_invite_gate", c.BetaInviteCode != ""),
		slog.Bool("gemini_configured", c.GeminiAPIKey != ""),
		slog.Bool("google_oauth_configured", c.GoogleClientID != ""),
		slog.String("upload_dir", c.UploadDir),
		slog.Bool("cookie_secure", c.CookieSecure),
		slog.String("app_base_url", c.AppBaseURL),
		slog.Int64("max_upload_bytes", c.MaxUploadBytes),
	)
}

// String prevents accidental printing of secret fields.
func (c *Config) String() string {
	return fmt.Sprintf("Config{Env:%s Port:%s AppBaseURL:%s UploadDir:%s CookieSecure:%v MaxUploadBytes:%d}",
		c.Env, c.Port, c.AppBaseURL, c.UploadDir, c.CookieSecure, c.MaxUploadBytes)
}

// Load reads environment variables, applies safe defaults, and validates required settings.
func Load() (*Config, error) {
	env := getEnv("ENV", defaultEnv)
	isProd := strings.EqualFold(env, "production")

	port := getEnv("PORT", defaultPort)
	if _, err := strconv.Atoi(port); err != nil {
		return nil, fmt.Errorf("invalid PORT %q: %w", port, err)
	}

	dbURL, err := resolveDatabaseURL(isProd)
	if err != nil {
		return nil, err
	}

	secretKey := strings.TrimSpace(os.Getenv("SECRET_KEY"))
	if secretKey == "" {
		if isProd {
			return nil, errors.New("SECRET_KEY is required when ENV=production")
		}
		ephemeral, err := generateEphemeralSecret()
		if err != nil {
			return nil, fmt.Errorf("generate ephemeral dev secret: %w", err)
		}
		secretKey = ephemeral
		slog.Warn("SECRET_KEY not set; generated ephemeral key for development session signing")
	}

	expireMins, err := getEnvInt("ACCESS_TOKEN_EXPIRE_MINUTES", defaultAccessTokenExpireMinutes)
	if err != nil || expireMins <= 0 {
		return nil, fmt.Errorf("invalid ACCESS_TOKEN_EXPIRE_MINUTES: must be a positive integer")
	}

	maxUploadBytes, err := getEnvInt64("MAX_UPLOAD_BYTES", defaultMaxUploadBytes)
	if err != nil || maxUploadBytes <= 0 {
		return nil, fmt.Errorf("invalid MAX_UPLOAD_BYTES: must be a positive integer")
	}

	cookieSecure, err := getEnvBool("COOKIE_SECURE", isProd)
	if err != nil {
		return nil, fmt.Errorf("invalid COOKIE_SECURE: %w", err)
	}

	appBaseURL := strings.TrimRight(getEnv("APP_BASE_URL", "http://localhost:"+port), "/")
	if _, err := url.ParseRequestURI(appBaseURL); err != nil {
		return nil, fmt.Errorf("invalid APP_BASE_URL %q: %w", appBaseURL, err)
	}

	return &Config{
		Port:                     port,
		Env:                      strings.ToLower(env),
		DatabaseURL:              dbURL,
		SecretKey:                secretKey,
		AccessTokenExpireMinutes: expireMins,
		BetaInviteCode:           strings.TrimSpace(os.Getenv("BETA_INVITE_CODE")),
		GeminiAPIKey:             strings.TrimSpace(os.Getenv("GEMINI_API_KEY")),
		GoogleClientID:           strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID")),
		UploadDir:                getEnv("UPLOAD_DIR", defaultUploadDir),
		CookieSecure:             cookieSecure,
		AppBaseURL:               appBaseURL,
		MaxUploadBytes:           maxUploadBytes,
	}, nil
}

func resolveDatabaseURL(_ bool) (string, error) {
	if raw := strings.TrimSpace(os.Getenv("DB_PATH")); raw != "" {
		return raw, nil
	}
	if raw := strings.TrimSpace(os.Getenv("DATABASE_URL")); raw != "" {
		return raw, nil
	}
	return defaultDBPath, nil
}

func generateEphemeralSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func getEnv(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func getEnvInt64(key string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func getEnvBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseBool(raw)
}
