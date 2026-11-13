package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeadersMiddleware(t *testing.T) {
	h := HeadersMiddleware(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://syllabuddy.me/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff header")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("missing DENY header")
	}
	if rec.Header().Get("Cross-Origin-Opener-Policy") != "same-origin-allow-popups" {
		t.Fatal("unexpected COOP header")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "https://accounts.google.com") || !strings.Contains(csp, "https://apis.google.com") {
		t.Fatalf("CSP missing Google GSI origins: %s", csp)
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("missing HSTS on HTTPS production request")
	}
}

func TestRateLimiterBoundedAndEnforced(t *testing.T) {
	rl := NewRateLimiter(1, 2, 3)
	if !rl.Allow("1.1.1.1") || !rl.Allow("1.1.1.1") {
		t.Fatal("expected initial burst of 2 to pass")
	}
	if rl.Allow("1.1.1.1") {
		t.Fatal("expected 3rd immediate request to be rate limited")
	}

	// Add more IPs than maxEntries=3 and verify map stays bounded.
	rl.Allow("2.2.2.2")
	rl.Allow("3.3.3.3")
	rl.Allow("4.4.4.4")
	if len(rl.buckets) > 3 {
		t.Fatalf("expected bounded buckets <= 3, got %d", len(rl.buckets))
	}
}

func TestCSRFMiddleware(t *testing.T) {
	mw := CSRFMiddleware("https://syllabuddy.me", "syllabuddy_token")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Cookie mutation from cross-site origin must be blocked.
	reqCross := httptest.NewRequest(http.MethodPost, "https://syllabuddy.me/api/syllabi", nil)
	reqCross.AddCookie(&http.Cookie{Name: "syllabuddy_token", Value: "jwt"})
	reqCross.Header.Set("Origin", "https://evil.example")
	recCross := httptest.NewRecorder()
	mw.ServeHTTP(recCross, reqCross)
	if recCross.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin cookie mutation, got %d", recCross.Code)
	}

	// Cookie mutation from matching origin must succeed.
	reqSame := httptest.NewRequest(http.MethodPost, "https://syllabuddy.me/api/syllabi", nil)
	reqSame.AddCookie(&http.Cookie{Name: "syllabuddy_token", Value: "jwt"})
	reqSame.Header.Set("Origin", "https://syllabuddy.me")
	recSame := httptest.NewRecorder()
	mw.ServeHTTP(recSame, reqSame)
	if recSame.Code != http.StatusOK {
		t.Fatalf("expected 200 for same-origin cookie mutation, got %d", recSame.Code)
	}

	// Bearer-only mutation without cookie bypasses cookie CSRF gate.
	reqBearer := httptest.NewRequest(http.MethodPost, "https://syllabuddy.me/api/syllabi", nil)
	reqBearer.Header.Set("Authorization", "Bearer jwt")
	recBearer := httptest.NewRecorder()
	mw.ServeHTTP(recBearer, reqBearer)
	if recBearer.Code != http.StatusOK {
		t.Fatalf("expected 200 for bearer-only mutation, got %d", recBearer.Code)
	}
}
