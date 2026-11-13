package handlers

import (
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"

	"syllabuddy/internal/auth"
	"syllabuddy/internal/db"
	"syllabuddy/internal/models"
)

type registerRequest struct {
	Username   string `json:"username"`
	Email      string `json:"email"`
	Password   string `json:"password"`
	InviteCode string `json:"invite_code"`
}

type loginRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type googleAuthRequest struct {
	Credential string `json:"credential"`
	IDToken    string `json:"id_token"`
	Token      string `json:"token"`
}

type updateProfileRequest struct {
	Username string `json:"username"`
}

type updatePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type deleteAccountRequest struct {
	Confirm bool `json:"confirm"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(w, r, &req, defaultJSONBodyLimit); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	if expectedCode := strings.TrimSpace(s.Config.BetaInviteCode); expectedCode != "" {
		provided := strings.TrimSpace(req.InviteCode)
		if subtle.ConstantTimeCompare([]byte(expectedCode), []byte(provided)) != 1 {
			writeError(w, http.StatusForbidden, "invalid beta invite code")
			return
		}
	}

	username, err := validateUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	email, err := validateEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := s.DB.CreateUser(r.Context(), username, email, hash)
	if err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeError(w, http.StatusConflict, "username or email already exists")
			return
		}
		s.Logger.Error("create user failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to create account")
		return
	}

	s.respondWithSession(w, http.StatusCreated, user)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req, defaultJSONBodyLimit); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	identifier := strings.TrimSpace(req.Username)
	if identifier == "" {
		identifier = strings.TrimSpace(req.Email)
	}
	if identifier == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username/email and password are required")
		return
	}

	user, err := s.lookupUserByIdentifier(r, identifier)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid username/email or password")
			return
		}
		s.Logger.Error("login lookup failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid username/email or password")
		return
	}

	s.respondWithSession(w, http.StatusOK, user)
}

func (s *Server) lookupUserByIdentifier(r *http.Request, identifier string) (*models.User, error) {
	if strings.Contains(identifier, "@") {
		u, err := s.DB.GetUserByEmail(r.Context(), identifier)
		if err == nil || !errors.Is(err, db.ErrNotFound) {
			return u, err
		}
		return s.DB.GetUserByUsername(r.Context(), identifier)
	}
	u, err := s.DB.GetUserByUsername(r.Context(), identifier)
	if err == nil || !errors.Is(err, db.ErrNotFound) {
		return u, err
	}
	return s.DB.GetUserByEmail(r.Context(), identifier)
}

func (s *Server) handleGoogleAuth(w http.ResponseWriter, r *http.Request) {
	if s.Config.GoogleClientID == "" {
		writeError(w, http.StatusServiceUnavailable, "Google Sign-In is not configured")
		return
	}

	var req googleAuthRequest
	if err := decodeJSON(w, r, &req, defaultJSONBodyLimit); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	idToken := strings.TrimSpace(req.Credential)
	if idToken == "" {
		idToken = strings.TrimSpace(req.IDToken)
	}
	if idToken == "" {
		idToken = strings.TrimSpace(req.Token)
	}
	if idToken == "" {
		writeError(w, http.StatusBadRequest, "Google credential is required")
		return
	}

	claims, err := s.Auth.VerifyGoogleIDToken(r.Context(), idToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid Google ID token")
		return
	}

	user, err := s.DB.CreateOrLinkGoogleUser(r.Context(), claims.Name, claims.Email, claims.Sub)
	if err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeError(w, http.StatusConflict, "account conflict linking Google identity")
			return
		}
		s.Logger.Error("google user link failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "Google sign-in failed")
		return
	}

	s.respondWithSession(w, http.StatusOK, user)
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	s.Auth.ClearAuthCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	user, err := s.DB.GetUserByID(r.Context(), userCtx.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			s.Auth.ClearAuthCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		s.Logger.Error("get current user failed", slog.Int("user_id", userCtx.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to load profile")
		return
	}

	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req updateProfileRequest
	if err := decodeJSON(w, r, &req, defaultJSONBodyLimit); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	username, err := validateUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	updated, err := s.DB.UpdateUsername(r.Context(), userCtx.ID, username)
	if err != nil {
		if errors.Is(err, db.ErrConflict) {
			writeError(w, http.StatusConflict, "username is already taken")
			return
		}
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		s.Logger.Error("update username failed", slog.Int("user_id", userCtx.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to update profile")
		return
	}

	if tok, tokErr := s.Auth.IssueToken(models.UserContext{
		ID:       updated.ID,
		Username: updated.Username,
		Email:    updated.Email,
	}); tokErr == nil {
		s.Auth.SetAuthCookie(w, tok)
	}

	writeJSON(w, http.StatusOK, map[string]any{"user": updated})
}

func (s *Server) handleUpdatePassword(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req updatePasswordRequest
	if err := decodeJSON(w, r, &req, defaultJSONBodyLimit); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	}

	if err := validatePassword(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user, err := s.DB.GetUserByID(r.Context(), userCtx.ID)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		s.Logger.Error("load user for password update failed", slog.Int("user_id", userCtx.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.DB.UpdatePasswordHash(r.Context(), userCtx.ID, newHash); err != nil {
		s.Logger.Error("update password hash failed", slog.Int("user_id", userCtx.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "password updated"})
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	userCtx, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req deleteAccountRequest
	if err := decodeJSON(w, r, &req, defaultJSONBodyLimit); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request payload")
		return
	} else if err == nil && !req.Confirm {
		writeError(w, http.StatusBadRequest, "confirmation is required to delete account")
		return
	}

	syllabi, err := s.DB.ListSyllabiByUser(r.Context(), userCtx.ID)
	if err != nil {
		s.Logger.Error("list syllabi before account delete failed", slog.Int("user_id", userCtx.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}

	if err := s.DB.DeleteUser(r.Context(), userCtx.ID); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			s.Auth.ClearAuthCookie(w)
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		s.Logger.Error("delete user failed", slog.Int("user_id", userCtx.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to delete account")
		return
	}

	for i := range syllabi {
		s.removeSyllabusFiles(&syllabi[i])
	}

	s.Auth.ClearAuthCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"message": "account deleted"})
}

func (s *Server) respondWithSession(w http.ResponseWriter, status int, user *models.User) {
	token, err := s.Auth.IssueToken(models.UserContext{
		ID:       user.ID,
		Username: user.Username,
		Email:    user.Email,
	})
	if err != nil {
		s.Logger.Error("issue session token failed", slog.Int("user_id", user.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	s.Auth.SetAuthCookie(w, token)
	writeJSON(w, status, map[string]any{
		"user":         user,
		"token":        token,
		"access_token": token,
		"token_type":   "bearer",
	})
}

func validateUsername(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if len(u) < 3 || len(u) > 50 {
		return "", errors.New("username must be between 3 and 50 characters")
	}
	for i := 0; i < len(u); i++ {
		c := u[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.'
		if !ok {
			return "", errors.New("username may only contain letters, numbers, '.', '_', and '-'")
		}
	}
	return u, nil
}

func validateEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" || len(e) > 120 {
		return "", errors.New("valid email address is required")
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || strings.ToLower(addr.Address) != e {
		return "", errors.New("valid email address is required")
	}
	at := strings.IndexByte(e, '@')
	if at <= 0 || !strings.Contains(e[at+1:], ".") {
		return "", errors.New("valid email address is required")
	}
	return e, nil
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len(password) > 72 {
		return errors.New("password must not exceed 72 bytes")
	}
	return nil
}
