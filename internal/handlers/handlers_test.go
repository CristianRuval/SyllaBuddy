package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"syllabuddy/internal/auth"
	"syllabuddy/internal/config"
	"syllabuddy/internal/db"
	"syllabuddy/internal/logger"
	"syllabuddy/internal/models"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	uploadDir := t.TempDir()
	cfg := &config.Config{
		Port:                     "8080",
		Env:                      "development",
		SecretKey:                "test-secret-key-at-least-32-bytes-long",
		AccessTokenExpireMinutes: 60,
		BetaInviteCode:           "UTD-BETA",
		UploadDir:                uploadDir,
		AppBaseURL:               "http://localhost:8080",
		MaxUploadBytes:           15 * 1024 * 1024,
	}
	authMgr := auth.NewManager(cfg.SecretKey, time.Hour, false, "test-google-client-id")
	// Use a non-nil placeholder DB pointer only for routes that don't query DB;
	// template/health/page-auth tests never dereference DB.
	var database db.DB
	srv, err := NewServer(cfg, logger.New("development"), &database, authMgr, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Shutdown(t.Context())
	})
	return srv
}

func TestParseAndExecuteAllTemplates(t *testing.T) {
	srv := newTestServer(t)

	pages := []struct {
		name      string
		activeNav string
		user      *models.UserContext
	}{
		{"landing.html", "landing", nil},
		{"landing.html", "landing", &models.UserContext{ID: 1, Username: "temoc", Email: "temoc@utdallas.edu"}},
		{"login.html", "login", nil},
		{"privacy.html", "privacy", nil},
		{"terms.html", "terms", nil},
		{"dashboard.html", "dashboard", &models.UserContext{ID: 1, Username: "temoc", Email: "temoc@utdallas.edu"}},
		{"syllabi.html", "syllabi", &models.UserContext{ID: 1, Username: "temoc", Email: "temoc@utdallas.edu"}},
		{"calendar.html", "calendar", &models.UserContext{ID: 1, Username: "temoc", Email: "temoc@utdallas.edu"}},
		{"settings.html", "settings", &models.UserContext{ID: 1, Username: "temoc", Email: "temoc@utdallas.edu"}},
	}

	for _, tc := range pages {
		tmpl, ok := srv.Templates[tc.name]
		if !ok {
			t.Fatalf("missing parsed template %q", tc.name)
		}
		var buf bytes.Buffer
		data := pageData{
			PageTitle:         "Test",
			ActiveNav:         tc.activeNav,
			User:              tc.user,
			GoogleClientID:    "test-client",
			RequireInviteCode: true,
			SyllabusID:        42,
		}
		if err := tmpl.ExecuteTemplate(&buf, "layout.html", data); err != nil {
			t.Fatalf("ExecuteTemplate(%q) failed: %v", tc.name, err)
		}
		if buf.Len() == 0 {
			t.Fatalf("ExecuteTemplate(%q) produced empty output", tc.name)
		}
	}
}

func TestRoutesHealthStaticAndAuthGuards(t *testing.T) {
	srv := newTestServer(t)
	handler := srv.Routes()

	// 1. /healthz returns 200 JSON
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatalf("expected Content-Security-Policy header on response")
	}

	// 2. /static/css/app.css served from embedded FS
	req = httptest.NewRequest(http.MethodGet, "/static/css/app.css", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/css/app.css status = %d, want 200", rec.Code)
	}

	// 3. Unauthenticated /dashboard redirects to /login
	req = httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("GET /dashboard unauthenticated = (%d, %q), want (303, /login)", rec.Code, rec.Header().Get("Location"))
	}

	// 4. Authenticated /login redirects to /dashboard
	tok, err := srv.Auth.IssueToken(models.UserContext{ID: 7, Username: "temoc", Email: "temoc@utdallas.edu"})
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/login", nil)
	req.AddCookie(&http.Cookie{Name: auth.AuthCookieName, Value: tok})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard" {
		t.Fatalf("GET /login authenticated = (%d, %q), want (303, /dashboard)", rec.Code, rec.Header().Get("Location"))
	}

	// 5. Unauthenticated /api/syllabi returns 401 JSON
	req = httptest.NewRequest(http.MethodGet, "/api/syllabi", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/syllabi unauthenticated = %d, want 401", rec.Code)
	}

	// 6. Invite code mismatch on /api/auth/register returns 403
	req = httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewBufferString(`{"username":"temoc","email":"temoc@utdallas.edu","password":"Password123","invite_code":"WRONG"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /api/auth/register wrong invite code = %d, want 403", rec.Code)
	}
}

func TestValidationAndPathHelpers(t *testing.T) {
	srv := newTestServer(t)

	if _, err := validateUsername("ab"); err == nil {
		t.Fatal("expected error for short username")
	}
	if _, err := validateUsername("bad user"); err == nil {
		t.Fatal("expected error for whitespace in username")
	}
	if u, err := validateUsername("temoc_26"); err != nil || u != "temoc_26" {
		t.Fatalf("validateUsername valid failed: %v %q", err, u)
	}

	if _, err := validateEmail("not-an-email"); err == nil {
		t.Fatal("expected error for invalid email")
	}
	if e, err := validateEmail("Temoc@UTDallas.edu"); err != nil || e != "temoc@utdallas.edu" {
		t.Fatalf("validateEmail valid failed: %v %q", err, e)
	}

	if err := validatePassword("short"); err == nil {
		t.Fatal("expected error for <8 char password")
	}
	if err := validatePassword("validpass1"); err != nil {
		t.Fatalf("validatePassword valid failed: %v", err)
	}

	if _, ok := parseYYYYMMDD("2026-13-01"); ok {
		t.Fatal("expected invalid month to fail")
	}
	if tm, ok := parseYYYYMMDD("2026-10-14"); !ok || tm.Format("2006-01-02") != "2026-10-14" {
		t.Fatal("expected valid YYYY-MM-DD to parse")
	}

	inside := filepath.Join(srv.Config.UploadDir, "user_1_test.pdf")
	if _, err := srv.resolveUploadPath(inside); err != nil {
		t.Fatalf("resolveUploadPath inside failed: %v", err)
	}
	outside := filepath.Join(srv.Config.UploadDir, "..", "escape.pdf")
	if _, err := srv.resolveUploadPath(outside); err == nil {
		t.Fatal("expected path traversal outside UploadDir to be rejected")
	}
}

func TestGoogleSyncDeduplicationICalUID(t *testing.T) {
	uid1 := googleEventICalUID(1, 10, "Midterm 1", "exam", 0)
	uid1Same := googleEventICalUID(1, 10, "  midterm 1 ", "EXAM", 0)
	uid1DupTitle := googleEventICalUID(1, 10, "Midterm 1", "exam", 1)
	uidOtherSyl := googleEventICalUID(1, 11, "Midterm 1", "exam", 0)

	if uid1 != uid1Same {
		t.Fatalf("expected normalized deterministic iCalUID, got %q vs %q", uid1, uid1Same)
	}
	if !strings.HasPrefix(uid1, "sb-") || !strings.HasSuffix(uid1, "@syllabuddy.me") {
		t.Fatalf("unexpected iCalUID format: %q", uid1)
	}
	if uid1 == uid1DupTitle || uid1 == uidOtherSyl {
		t.Fatalf("expected distinct iCalUIDs for duplicate index/syllabus")
	}

	ctx := context.Background()
	database, err := db.Connect(ctx, filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatalf("db.Connect() error = %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("db.Migrate() error = %v", err)
	}

	u, err := database.CreateUser(ctx, "temoc", "temoc@utdallas.edu", "hashed")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	_, err = database.CreateSyllabus(ctx, u.ID, "CS 4349", filepath.Join(t.TempDir(), "s1.pdf"), &models.ParserOutput{
		Events: []models.SyllabusEvent{
			{Title: "Midterm 1", Type: "exam", Date: "2026-10-14"},
			{Title: "Midterm 1", Type: "exam", Date: "2026-11-11"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSyllabus() error = %v", err)
	}

	var receivedUIDs []string

	cfg := &config.Config{
		Port:                     "8080",
		Env:                      "development",
		SecretKey:                "test-secret-key-at-least-32-bytes-long",
		AccessTokenExpireMinutes: 60,
		UploadDir:                t.TempDir(),
		AppBaseURL:               "http://localhost:8080",
		MaxUploadBytes:           15 * 1024 * 1024,
	}
	authMgr := auth.NewManager(cfg.SecretKey, time.Hour, false, "")
	srv, err := NewServer(cfg, logger.New("development"), database, authMgr, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(t.Context()) })

	srv.googleClient = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != googleCalendarEventsImportURL {
				t.Fatalf("unexpected Google Calendar URL: %q", r.URL.String())
			}
			var body struct {
				ICalUID string `json:"iCalUID"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode mock google request: %v", err)
			}
			receivedUIDs = append(receivedUIDs, body.ICalUID)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
				Header:     make(http.Header),
			}, nil
		}),
	}

	tok, err := srv.Auth.IssueToken(models.UserContext{ID: u.ID, Username: u.Username, Email: u.Email})
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	handler := srv.Routes()
	syncOnce := func() {
		req := httptest.NewRequest(http.MethodPost, "/api/events/sync-google", bytes.NewBufferString(`{"google_token":"tok"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost:8080")
		req.AddCookie(&http.Cookie{Name: auth.AuthCookieName, Value: tok})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("sync-google status = %d, body = %s", rec.Code, rec.Body.String())
		}
	}

	syncOnce()
	syncOnce()

	if len(receivedUIDs) != 4 {
		t.Fatalf("expected 4 total event upsert calls across 2 syncs, got %d", len(receivedUIDs))
	}
	if receivedUIDs[0] == receivedUIDs[1] {
		t.Fatalf("expected distinct iCalUIDs for two events in same syllabus, both were %q", receivedUIDs[0])
	}
	if receivedUIDs[0] != receivedUIDs[2] || receivedUIDs[1] != receivedUIDs[3] {
		t.Fatalf("expected identical iCalUIDs on second sync, got %v", receivedUIDs)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
