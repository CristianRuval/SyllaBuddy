package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"syllabuddy/internal/models"
)

func TestPasswordHashAndCheck(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if !CheckPassword(hash, "correct-horse-battery") {
		t.Fatal("expected password check to pass")
	}
	if CheckPassword(hash, "wrong-password") {
		t.Fatal("expected wrong password check to fail")
	}
	if CheckPassword("!google-oauth-only!", "anything") {
		t.Fatal("expected sentinel hash to reject password login")
	}
}

func TestIssueVerifyAndMiddleware(t *testing.T) {
	mgr := NewManager("test-secret-key-at-least-32-bytes", time.Hour, false, "client-id")
	tok, err := mgr.IssueToken(models.UserContext{ID: 42, Username: "temoc", Email: "temoc@utdallas.edu"})
	if err != nil {
		t.Fatalf("IssueToken failed: %v", err)
	}

	user, err := mgr.VerifyToken(tok)
	if err != nil {
		t.Fatalf("VerifyToken failed: %v", err)
	}
	if user.ID != 42 || user.Username != "temoc" || user.Email != "temoc@utdallas.edu" {
		t.Fatalf("unexpected user claims: %+v", user)
	}

	// Cookie auth
	handler := mgr.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok || u.ID != 42 {
			t.Fatalf("missing user in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	reqCookie := httptest.NewRequest(http.MethodGet, "/api/syllabi", nil)
	reqCookie.AddCookie(&http.Cookie{Name: AuthCookieName, Value: tok})
	recCookie := httptest.NewRecorder()
	handler.ServeHTTP(recCookie, reqCookie)
	if recCookie.Code != http.StatusOK {
		t.Fatalf("expected 200 with cookie auth, got %d", recCookie.Code)
	}

	// Bearer fallback
	reqBearer := httptest.NewRequest(http.MethodGet, "/api/syllabi", nil)
	reqBearer.Header.Set("Authorization", "Bearer "+tok)
	recBearer := httptest.NewRecorder()
	handler.ServeHTTP(recBearer, reqBearer)
	if recBearer.Code != http.StatusOK {
		t.Fatalf("expected 200 with bearer auth, got %d", recBearer.Code)
	}

	// Unauthenticated
	reqAnon := httptest.NewRequest(http.MethodGet, "/api/syllabi", nil)
	recAnon := httptest.NewRecorder()
	handler.ServeHTTP(recAnon, reqAnon)
	if recAnon.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when unauthenticated, got %d", recAnon.Code)
	}
}
