package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"syllabuddy/internal/auth"
	"syllabuddy/internal/models"
)

type pageData struct {
	PageTitle         string
	ActiveNav         string
	User              *models.UserContext
	GoogleClientID    string
	RequireInviteCode bool
	Theme             string
	SyllabusID        int
}

func (s *Server) basePageData(r *http.Request, title, activeNav string) pageData {
	user, _ := auth.UserFromContext(r.Context())
	return pageData{
		PageTitle:         title,
		ActiveNav:         activeNav,
		User:              user,
		GoogleClientID:    s.Config.GoogleClientID,
		RequireInviteCode: s.Config.BetaInviteCode != "",
		Theme:             "",
	}
}

func (s *Server) renderPage(w http.ResponseWriter, templateName string, data pageData) {
	tmpl, ok := s.Templates[templateName]
	if !ok {
		s.Logger.Error("template not found", slog.String("template", templateName))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		s.Logger.Error("render template failed", slog.String("template", templateName), slog.String("error", err.Error()))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleLandingPage(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "SyllaBuddy — AI Syllabus Tracking & Calendar Workspace", "landing")
	s.renderPage(w, "landing.html", data)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.UserFromContext(r.Context()); ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	data := s.basePageData(r, "Sign In — SyllaBuddy", "login")
	s.renderPage(w, "login.html", data)
}

func (s *Server) handlePrivacyPage(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "Privacy Policy — SyllaBuddy", "privacy")
	s.renderPage(w, "privacy.html", data)
}

func (s *Server) handleTermsPage(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "Terms of Service — SyllaBuddy", "terms")
	s.renderPage(w, "terms.html", data)
}

func (s *Server) handleDashboardPage(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "Overview — SyllaBuddy", "dashboard")
	s.renderPage(w, "dashboard.html", data)
}

func (s *Server) handleSyllabiPage(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "Syllabi Library — SyllaBuddy", "syllabi")
	s.renderPage(w, "syllabi.html", data)
}

func (s *Server) handleCalendarPage(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "Semester Calendar — SyllaBuddy", "calendar")
	if rawID := strings.TrimSpace(r.PathValue("id")); rawID != "" {
		if id, err := strconv.Atoi(rawID); err == nil && id > 0 {
			data.SyllabusID = id
		}
	} else if rawQueryID := strings.TrimSpace(r.URL.Query().Get("syllabus_id")); rawQueryID != "" {
		if id, err := strconv.Atoi(rawQueryID); err == nil && id > 0 {
			data.SyllabusID = id
		}
	}
	s.renderPage(w, "calendar.html", data)
}

func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	data := s.basePageData(r, "Account Settings — SyllaBuddy", "settings")
	s.renderPage(w, "settings.html", data)
}
