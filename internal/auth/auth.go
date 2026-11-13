package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"syllabuddy/internal/logger"
	"syllabuddy/internal/models"
)

const (
	// AuthCookieName is the HttpOnly cookie carrying the signed session JWT.
	AuthCookieName = "syllabuddy_token"

	googleTokenInfoEndpoint = "https://oauth2.googleapis.com/tokeninfo"
	maxGoogleRespBytes      = 32 * 1024
)

var (
	// ErrUnauthorized indicates missing, malformed, or expired credentials.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrInvalidGoogleToken indicates Google ID token verification failed.
	ErrInvalidGoogleToken = errors.New("invalid google id token")
)

type ctxKey int

const userCtxKey ctxKey = iota

// GoogleClaims contains verified identity fields from a Google ID token.
type GoogleClaims struct {
	Sub           string
	Email         string
	EmailVerified string
	Name          string
	Picture       string
	Aud           string
	Iss           string
}

type claims struct {
	UserID int    `json:"user_id"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// Manager handles password hashing, JWT issuance/verification, cookies, auth middleware, and Google OAuth verification.
type Manager struct {
	secretKey      []byte
	tokenTTL       time.Duration
	cookieSecure   bool
	googleClientID string
	httpClient     *http.Client
}

// NewManager constructs an auth Manager.
func NewManager(secretKey string, tokenTTL time.Duration, cookieSecure bool, googleClientID string) *Manager {
	if tokenTTL <= 0 {
		tokenTTL = 24 * time.Hour
	}
	return &Manager{
		secretKey:      []byte(secretKey),
		tokenTTL:       tokenTTL,
		cookieSecure:   cookieSecure,
		googleClientID: strings.TrimSpace(googleClientID),
		httpClient: &http.Client{
			Timeout: 8 * time.Second,
		},
	}
}

// HashPassword generates a bcrypt hash of password.
func HashPassword(password string) (string, error) {
	if len(password) < 6 {
		return "", errors.New("password must be at least 6 characters")
	}
	if len(password) > 72 {
		return "", errors.New("password must not exceed 72 bytes")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hashed), nil
}

// CheckPassword verifies a plaintext password against a stored bcrypt hash.
func CheckPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// IssueToken signs an HS256 JWT for the given user context.
func (m *Manager) IssueToken(user models.UserContext) (string, error) {
	if len(m.secretKey) == 0 {
		return "", errors.New("signing key is not configured")
	}
	if user.ID <= 0 || user.Username == "" {
		return "", errors.New("invalid user context for token issuance")
	}

	now := time.Now().UTC()
	c := claims{
		UserID: user.ID,
		Email:  user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.Username,
			Issuer:    "syllabuddy",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.tokenTTL)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	signed, err := tok.SignedString(m.secretKey)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// VerifyToken validates an HS256 JWT and returns the authenticated UserContext.
func (m *Manager) VerifyToken(tokenStr string) (*models.UserContext, error) {
	tokenStr = strings.TrimSpace(tokenStr)
	if tokenStr == "" || len(m.secretKey) == 0 {
		return nil, ErrUnauthorized
	}

	var parsedClaims claims
	tok, err := jwt.ParseWithClaims(
		tokenStr,
		&parsedClaims,
		func(t *jwt.Token) (any, error) {
			return m.secretKey, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !tok.Valid {
		return nil, ErrUnauthorized
	}
	if parsedClaims.UserID <= 0 || parsedClaims.Subject == "" {
		return nil, ErrUnauthorized
	}

	return &models.UserContext{
		ID:       parsedClaims.UserID,
		Username: parsedClaims.Subject,
		Email:    parsedClaims.Email,
	}, nil
}

// SetAuthCookie writes the session JWT to an HttpOnly SameSite=Lax cookie.
func (m *Manager) SetAuthCookie(w http.ResponseWriter, token string) {
	maxAge := int(m.tokenTTL.Seconds())
	if maxAge <= 0 {
		maxAge = 86400
	}
	http.SetCookie(w, &http.Cookie{
		Name:     AuthCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		Expires:  time.Now().UTC().Add(m.tokenTTL),
		HttpOnly: true,
		Secure:   m.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearAuthCookie expires the session auth cookie immediately.
func (m *Manager) ClearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     AuthCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		HttpOnly: true,
		Secure:   m.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// RequireAuth authenticates the request from cookie first, then Authorization: Bearer, returning 401 on failure.
func (m *Manager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := m.authenticateRequest(r)
		if !ok {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		logger.SetUserID(r.Context(), user.ID)
		ctx := WithUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePageAuth authenticates HTML page requests and redirects unauthenticated browsers to redirectTo.
func (m *Manager) RequirePageAuth(redirectTo string) func(http.Handler) http.Handler {
	if redirectTo == "" {
		redirectTo = "/"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := m.authenticateRequest(r)
			if !ok {
				http.Redirect(w, r, redirectTo, http.StatusSeeOther)
				return
			}
			logger.SetUserID(r.Context(), user.ID)
			ctx := WithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalAuth attaches UserContext if a valid cookie or Bearer token is present, otherwise continues anonymously.
func (m *Manager) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, ok := m.authenticateRequest(r); ok {
			logger.SetUserID(r.Context(), user.ID)
			ctx := WithUser(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WithUser stores UserContext in ctx.
func WithUser(ctx context.Context, user *models.UserContext) context.Context {
	return context.WithValue(ctx, userCtxKey, user)
}

// UserFromContext retrieves the authenticated UserContext from ctx.
func UserFromContext(ctx context.Context) (*models.UserContext, bool) {
	if ctx == nil {
		return nil, false
	}
	u, ok := ctx.Value(userCtxKey).(*models.UserContext)
	if !ok || u == nil {
		return nil, false
	}
	return u, true
}

func (m *Manager) authenticateRequest(r *http.Request) (*models.UserContext, bool) {
	if c, err := r.Cookie(AuthCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
		if user, verErr := m.VerifyToken(c.Value); verErr == nil {
			return user, true
		}
	}

	if authHeader := strings.TrimSpace(r.Header.Get("Authorization")); authHeader != "" {
		if len(authHeader) > 7 && strings.EqualFold(authHeader[:7], "Bearer ") {
			bearer := strings.TrimSpace(authHeader[7:])
			if user, verErr := m.VerifyToken(bearer); verErr == nil {
				return user, true
			}
		}
	}

	return nil, false
}

// VerifyGoogleIDToken validates a Google Identity Services ID token via Google's tokeninfo endpoint.
func (m *Manager) VerifyGoogleIDToken(ctx context.Context, idToken string) (*GoogleClaims, error) {
	if m.googleClientID == "" {
		return nil, errors.New("google oauth client id is not configured")
	}
	idToken = strings.TrimSpace(idToken)
	if idToken == "" {
		return nil, ErrInvalidGoogleToken
	}

	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	form := url.Values{}
	form.Set("id_token", idToken)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, googleTokenInfoEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create google tokeninfo request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google tokeninfo request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrInvalidGoogleToken
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGoogleRespBytes))
	if err != nil {
		return nil, fmt.Errorf("read google tokeninfo response: %w", err)
	}

	var payload struct {
		Sub           string          `json:"sub"`
		Email         string          `json:"email"`
		EmailVerified json.RawMessage `json:"email_verified"`
		Name          string          `json:"name"`
		Picture       string          `json:"picture"`
		Aud           string          `json:"aud"`
		Iss           string          `json:"iss"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ErrInvalidGoogleToken
	}

	emailVerified := strings.Trim(strings.TrimSpace(string(payload.EmailVerified)), `"`)
	if subtle.ConstantTimeCompare([]byte(payload.Aud), []byte(m.googleClientID)) != 1 {
		return nil, ErrInvalidGoogleToken
	}
	if emailVerified != "true" {
		return nil, ErrInvalidGoogleToken
	}
	if payload.Iss != "" && payload.Iss != "accounts.google.com" && payload.Iss != "https://accounts.google.com" {
		return nil, ErrInvalidGoogleToken
	}
	if strings.TrimSpace(payload.Sub) == "" || strings.TrimSpace(payload.Email) == "" {
		return nil, ErrInvalidGoogleToken
	}

	return &GoogleClaims{
		Sub:           payload.Sub,
		Email:         strings.ToLower(strings.TrimSpace(payload.Email)),
		EmailVerified: emailVerified,
		Name:          strings.TrimSpace(payload.Name),
		Picture:       strings.TrimSpace(payload.Picture),
		Aud:           payload.Aud,
		Iss:           payload.Iss,
	}, nil
}
