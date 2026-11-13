package logger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	requestMetaKey
)

// RequestMeta holds mutable per-request metadata populated by downstream middlewares (e.g. auth).
type RequestMeta struct {
	userID atomic.Int64
}

// New constructs a structured slog.Logger configured as JSON in production and text in development.
func New(env string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	var handler slog.Handler
	if strings.EqualFold(strings.TrimSpace(env), "production") {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	l := slog.New(handler)
	slog.SetDefault(l)
	return l
}

// WithRequestID returns a child context containing the given request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext extracts the request ID from context, or returns "".
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// SetUserID records the authenticated user ID on the request's logging metadata.
func SetUserID(ctx context.Context, userID int) {
	if ctx == nil || userID <= 0 {
		return
	}
	if meta, ok := ctx.Value(requestMetaKey).(*RequestMeta); ok && meta != nil {
		meta.userID.Store(int64(userID))
	}
}

// UserIDFromMeta reads the logged user ID from context metadata, or 0 if anonymous.
func UserIDFromMeta(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	if meta, ok := ctx.Value(requestMetaKey).(*RequestMeta); ok && meta != nil {
		return int(meta.userID.Load())
	}
	return 0
}

// RequestIDMiddleware attaches a validated or newly generated X-Request-ID to every request and response.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := sanitizeRequestID(r.Header.Get("X-Request-ID"))
		if reqID == "" {
			reqID = generateRequestID()
		}
		w.Header().Set("X-Request-ID", reqID)
		ctx := WithRequestID(r.Context(), reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AccessLogMiddleware logs structured HTTP request details without secrets, tokens, or query strings.
func AccessLogMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			meta := &RequestMeta{}
			ctx := context.WithValue(r.Context(), requestMetaKey, meta)
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r.WithContext(ctx))

			durationMs := float64(time.Since(start).Microseconds()) / 1000.0
			attrs := []any{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Float64("duration_ms", durationMs),
				slog.String("remote_ip", ClientIP(r)),
				slog.String("request_id", RequestIDFromContext(ctx)),
				slog.Int64("user_id", meta.userID.Load()),
			}
			if rec.status >= http.StatusInternalServerError {
				log.Error("http_request", attrs...)
			} else if rec.status >= http.StatusBadRequest {
				log.Warn("http_request", attrs...)
			} else {
				log.Info("http_request", attrs...)
			}
		})
	}
}

// RecoveryMiddleware catches handler panics, logs structured diagnostics, and returns a safe 500 response.
func RecoveryMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic_recovered",
						slog.Any("panic", rec),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.String("request_id", RequestIDFromContext(r.Context())),
						slog.String("stack", string(debug.Stack())),
					)
					if wantsJSON(r) {
						w.Header().Set("Content-Type", "application/json; charset=utf-8")
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = w.Write([]byte(`{"error":"internal server error"}`))
						return
					}
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP extracts the caller IP address, trusting proxy headers only when RemoteAddr is loopback or private.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	remoteIP := net.ParseIP(host)

	if remoteIP != nil && (remoteIP.IsLoopback() || remoteIP.IsPrivate()) {
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				candidate := strings.TrimSpace(parts[0])
				if parsed := net.ParseIP(candidate); parsed != nil {
					return parsed.String()
				}
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			if parsed := net.ParseIP(xri); parsed != nil {
				return parsed.String()
			}
		}
	}

	if remoteIP != nil {
		return remoteIP.String()
	}
	return host
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sr *statusRecorder) WriteHeader(code int) {
	if sr.wroteHeader {
		return
	}
	sr.status = code
	sr.wroteHeader = true
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if !sr.wroteHeader {
		sr.WriteHeader(http.StatusOK)
	}
	return sr.ResponseWriter.Write(b)
}

func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

func sanitizeRequestID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 64 {
		return ""
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !isAlphaNum && c != '-' && c != '_' && c != '.' {
			return ""
		}
	}
	return raw
}

func generateRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req-fallback"
	}
	return hex.EncodeToString(b[:])
}

func wantsJSON(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}
