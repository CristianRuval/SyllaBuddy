package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"syllabuddy/internal/auth"
	"syllabuddy/internal/config"
	"syllabuddy/internal/db"
	"syllabuddy/internal/gemini"
	"syllabuddy/internal/logger"
	"syllabuddy/internal/models"
	"syllabuddy/internal/security"
	"syllabuddy/web"
)

const (
	parseWorkerCount     = 2
	parseQueueCapacity   = 64
	parseJobTimeout      = 75 * time.Second
	defaultJSONBodyLimit = 64 * 1024
)

type parseJob struct {
	SyllabusID int
	UserID     int
	FilePath   string
	Fallback   string
}

// Server wires configuration, dependencies, templates, middlewares, and the bounded background parser pool.
type Server struct {
	Config       *config.Config
	Logger       *slog.Logger
	DB           *db.DB
	Auth         *auth.Manager
	GeminiParser *gemini.Parser
	Templates    map[string]*template.Template

	generalLimiter *security.RateLimiter
	strictLimiter  *security.RateLimiter
	googleClient   *http.Client

	parseQueue   chan parseJob
	workerCtx    context.Context
	workerCancel context.CancelFunc
	workerWg     sync.WaitGroup
	queueMu      sync.RWMutex
	closed       atomic.Bool
	closeOnce    sync.Once
}

// NewServer constructs a Server, parses embedded HTML templates, and starts the 2-worker PDF parser pool.
func NewServer(
	cfg *config.Config,
	log *slog.Logger,
	database *db.DB,
	authMgr *auth.Manager,
	parser *gemini.Parser,
) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("handlers: config is required")
	}
	if log == nil {
		log = slog.Default()
	}
	if database == nil {
		return nil, errors.New("handlers: db is required")
	}
	if authMgr == nil {
		return nil, errors.New("handlers: auth manager is required")
	}

	templates, err := parseAllTemplates()
	if err != nil {
		return nil, err
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())

	s := &Server{
		Config:         cfg,
		Logger:         log,
		DB:             database,
		Auth:           authMgr,
		GeminiParser:   parser,
		Templates:      templates,
		generalLimiter: security.NewRateLimiter(15, 45, 10000),
		strictLimiter:  security.NewRateLimiter(2, 10, 10000),
		googleClient:   &http.Client{Timeout: 10 * time.Second},
		parseQueue:     make(chan parseJob, parseQueueCapacity),
		workerCtx:      workerCtx,
		workerCancel:   workerCancel,
	}

	s.startParseWorkers()
	return s, nil
}

func parseAllTemplates() (map[string]*template.Template, error) {
	pages := []string{
		"landing.html",
		"login.html",
		"privacy.html",
		"terms.html",
		"dashboard.html",
		"syllabi.html",
		"calendar.html",
		"settings.html",
	}

	out := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		tmpl, err := template.ParseFS(web.TemplatesFS, "templates/layout.html", "templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", page, err)
		}
		out[page] = tmpl
	}
	return out, nil
}

func (s *Server) startParseWorkers() {
	for i := 0; i < parseWorkerCount; i++ {
		s.workerWg.Add(1)
		go s.runParseWorker(i + 1)
	}
}

func (s *Server) runParseWorker(workerID int) {
	defer s.workerWg.Done()
	for job := range s.parseQueue {
		s.processParseJob(workerID, job)
	}
}

func (s *Server) enqueueParseJob(job parseJob) bool {
	s.queueMu.RLock()
	defer s.queueMu.RUnlock()

	if s.closed.Load() {
		return false
	}

	select {
	case s.parseQueue <- job:
		return true
	default:
		return false
	}
}

func (s *Server) processParseJob(workerID int, job parseJob) {
	ctx, cancel := context.WithTimeout(s.workerCtx, parseJobTimeout)
	defer cancel()

	if s.GeminiParser == nil || strings.TrimSpace(s.Config.GeminiAPIKey) == "" {
		s.Logger.Warn("skipping syllabus parse: gemini API key not configured",
			slog.Int("worker_id", workerID),
			slog.Int("syllabus_id", job.SyllabusID),
		)
		s.storeFailedParserOutput(ctx, job, "GEMINI_API_KEY is not configured on the server.")
		return
	}

	pdfBytes, err := os.ReadFile(job.FilePath)
	if err != nil {
		s.Logger.Error("read syllabus pdf for parsing failed",
			slog.Int("worker_id", workerID),
			slog.Int("syllabus_id", job.SyllabusID),
			slog.String("error", err.Error()),
		)
		s.storeFailedParserOutput(ctx, job, "Could not read uploaded PDF from storage.")
		return
	}

	parsed, derivedCourse, err := s.GeminiParser.ParsePDF(ctx, pdfBytes)
	if err != nil {
		s.Logger.Error("gemini syllabus parse failed",
			slog.Int("worker_id", workerID),
			slog.Int("syllabus_id", job.SyllabusID),
			slog.String("error", err.Error()),
		)
		s.storeFailedParserOutput(ctx, job, friendlyParseError(err))
		return
	}

	courseName := strings.TrimSpace(derivedCourse)
	if courseName == "" {
		courseName = job.Fallback
	}

	if err := s.DB.UpdateSyllabusParserOutput(ctx, job.SyllabusID, job.UserID, courseName, parsed); err != nil {
		if !errors.Is(err, db.ErrNotFound) {
			s.Logger.Error("save parsed syllabus output failed",
				slog.Int("worker_id", workerID),
				slog.Int("syllabus_id", job.SyllabusID),
				slog.String("error", err.Error()),
			)
		}
		return
	}

	eventCount := 0
	if parsed != nil {
		eventCount = len(parsed.Events)
	}
	s.Logger.Info("syllabus parsed successfully",
		slog.Int("worker_id", workerID),
		slog.Int("syllabus_id", job.SyllabusID),
		slog.Int("events", eventCount),
	)
}

func (s *Server) storeFailedParserOutput(ctx context.Context, job parseJob, reason string) {
	failed := &models.ParserOutput{
		Events: []models.SyllabusEvent{},
		Error:  strings.TrimSpace(reason),
	}
	if err := s.DB.UpdateSyllabusParserOutput(ctx, job.SyllabusID, job.UserID, job.Fallback, failed); err != nil {
		if !errors.Is(err, db.ErrNotFound) {
			s.Logger.Warn("failed to store fallback parser output",
				slog.Int("syllabus_id", job.SyllabusID),
				slog.String("error", err.Error()),
			)
		}
	}
}

func friendlyParseError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "API_KEY_INVALID") || strings.Contains(msg, "API key not valid"):
		return "Gemini API key is invalid. Update GEMINI_API_KEY in .env and click Retry Parse."
	case strings.Contains(msg, "RESOURCE_EXHAUSTED") || strings.Contains(msg, "429"):
		return "Gemini API rate/quota limit reached. Please wait a moment and click Retry Parse."
	case strings.Contains(msg, "deadline exceeded"):
		return "Gemini AI request timed out. Click Retry Parse to try again."
	default:
		return "AI extraction failed. Check server logs or click Retry Parse."
	}
}

// Shutdown stops accepting new background parse jobs and waits for in-flight workers until ctx expires.
func (s *Server) Shutdown(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.queueMu.Lock()
		s.closed.Store(true)
		close(s.parseQueue)
		s.queueMu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		s.workerWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.workerCancel()
		return nil
	case <-ctx.Done():
		s.workerCancel()
		<-done
		return ctx.Err()
	}
}

// Routes registers all SSR pages, static assets, health checks, and API endpoints wrapped in security middlewares.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Health & readiness probes
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// Embedded static assets (web.StaticFS contains static/*)
	staticFileServer := http.FileServer(http.FS(web.StaticFS))
	mux.Handle("GET /static/", s.withStaticCacheHeaders(staticFileServer))

	// Public & authenticated SSR pages
	mux.Handle("GET /{$}", s.Auth.OptionalAuth(http.HandlerFunc(s.handleLandingPage)))
	mux.Handle("GET /login", s.Auth.OptionalAuth(http.HandlerFunc(s.handleLoginPage)))
	mux.Handle("GET /privacy", s.Auth.OptionalAuth(http.HandlerFunc(s.handlePrivacyPage)))
	mux.Handle("GET /terms", s.Auth.OptionalAuth(http.HandlerFunc(s.handleTermsPage)))

	requirePage := s.Auth.RequirePageAuth("/login")
	mux.Handle("GET /dashboard", requirePage(http.HandlerFunc(s.handleDashboardPage)))
	mux.Handle("GET /dashboard/syllabi", requirePage(http.HandlerFunc(s.handleSyllabiPage)))
	mux.Handle("GET /dashboard/calendar", requirePage(http.HandlerFunc(s.handleCalendarPage)))
	mux.Handle("GET /dashboard/calendar/{id}", requirePage(http.HandlerFunc(s.handleCalendarPage)))
	mux.Handle("GET /dashboard/settings", requirePage(http.HandlerFunc(s.handleSettingsPage)))

	// Auth & user account endpoints
	mux.Handle("POST /api/auth/register", s.strictLimiter.Middleware(http.HandlerFunc(s.handleRegister)))
	mux.Handle("POST /api/auth/login", s.strictLimiter.Middleware(http.HandlerFunc(s.handleLogin)))
	mux.Handle("POST /api/auth/google", s.strictLimiter.Middleware(http.HandlerFunc(s.handleGoogleAuth)))
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)

	mux.Handle("GET /api/users/me", s.Auth.RequireAuth(http.HandlerFunc(s.handleGetMe)))
	mux.Handle("PUT /api/users/me/profile", s.Auth.RequireAuth(http.HandlerFunc(s.handleUpdateProfile)))
	mux.Handle("PUT /api/users/me/password", s.Auth.RequireAuth(http.HandlerFunc(s.handleUpdatePassword)))
	mux.Handle("DELETE /api/users/me", s.Auth.RequireAuth(http.HandlerFunc(s.handleDeleteAccount)))

	// Syllabi, events, thumbnails, PDF viewing, ICS export, and Google Calendar sync
	mux.Handle("GET /api/syllabi", s.Auth.RequireAuth(http.HandlerFunc(s.handleListSyllabi)))
	mux.Handle("POST /api/syllabi/upload", s.strictLimiter.Middleware(s.Auth.RequireAuth(http.HandlerFunc(s.handleUploadSyllabus))))
	mux.Handle("POST /api/syllabi/{id}/reparse", s.strictLimiter.Middleware(s.Auth.RequireAuth(http.HandlerFunc(s.handleReparseSyllabus))))
	mux.Handle("DELETE /api/syllabi/{id}", s.Auth.RequireAuth(http.HandlerFunc(s.handleDeleteSyllabus)))
	mux.Handle("GET /api/thumbnail/{id}", s.Auth.RequireAuth(http.HandlerFunc(s.handleGetThumbnail)))
	mux.Handle("GET /api/view-pdf/{id}", s.Auth.RequireAuth(http.HandlerFunc(s.handleViewPDF)))

	mux.Handle("GET /api/events/all", s.Auth.RequireAuth(http.HandlerFunc(s.handleGetAllEvents)))
	mux.Handle("GET /api/syllabi/{id}/events", s.Auth.RequireAuth(http.HandlerFunc(s.handleGetSyllabusEvents)))
	mux.Handle("PUT /api/syllabi/{id}/events", s.Auth.RequireAuth(http.HandlerFunc(s.handleUpdateSyllabusEvent)))
	mux.Handle("GET /api/download-calendar/all", s.Auth.RequireAuth(http.HandlerFunc(s.handleDownloadAllICS)))
	mux.Handle("GET /api/download-calendar/{id}", s.Auth.RequireAuth(http.HandlerFunc(s.handleDownloadSyllabusICS)))
	mux.Handle("POST /api/events/sync-google", s.Auth.RequireAuth(http.HandlerFunc(s.handleSyncGoogleCalendar)))

	isProd := s.Config.IsProduction()
	var handler http.Handler = mux
	handler = security.CSRFMiddleware(s.Config.AppBaseURL, auth.AuthCookieName)(handler)
	handler = s.generalLimiter.Middleware(handler)
	handler = security.HeadersMiddleware(isProd)(handler)
	handler = logger.AccessLogMiddleware(s.Logger)(handler)
	handler = logger.RequestIDMiddleware(handler)
	handler = logger.RecoveryMiddleware(s.Logger)(handler)
	return handler
}

func (s *Server) withStaticCacheHeaders(next http.Handler) http.Handler {
	cacheVal := "public, max-age=3600"
	if !s.Config.IsProduction() {
		cacheVal = "no-cache"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", cacheVal)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.DB.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- Shared Handler Helpers ---

func writeJSON(w http.ResponseWriter, status int, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to encode response"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = defaultJSONBodyLimit
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// Ensure no trailing garbage after the first JSON value.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("unexpected trailing JSON content")
	}
	return nil
}

func parsePositiveIntParam(r *http.Request, key string) (int, error) {
	raw := strings.TrimSpace(r.PathValue(key))
	if raw == "" {
		return 0, errors.New("missing path parameter")
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid integer id")
	}
	return id, nil
}

func (s *Server) thumbPathFor(syllabusID int) string {
	return filepath.Join(s.Config.UploadDir, "thumbs", fmt.Sprintf("thumb_%d.png", syllabusID))
}

func (s *Server) resolveUploadPath(storedPath string) (string, error) {
	if strings.TrimSpace(storedPath) == "" {
		return "", errors.New("empty file path")
	}
	baseAbs, err := filepath.Abs(s.Config.UploadDir)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(storedPath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("file path outside upload directory")
	}
	return targetAbs, nil
}

func (s *Server) removeSyllabusFiles(syl *models.Syllabus) {
	if syl == nil {
		return
	}
	if safePath, err := s.resolveUploadPath(syl.FilePath); err == nil {
		if rmErr := os.Remove(safePath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			s.Logger.Warn("failed to remove syllabus pdf", slog.Int("syllabus_id", syl.ID), slog.String("error", rmErr.Error()))
		}
	}
	thumbPath := s.thumbPathFor(syl.ID)
	if rmErr := os.Remove(thumbPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		s.Logger.Warn("failed to remove syllabus thumbnail", slog.Int("syllabus_id", syl.ID), slog.String("error", rmErr.Error()))
	}
}

func randomHex(nBytes int) string {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}
