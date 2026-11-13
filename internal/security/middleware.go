package security

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"syllabuddy/internal/logger"
)

const (
	defaultMaxRateLimitEntries = 10000
	defaultBucketStaleTTL      = 10 * time.Minute
	defaultAuthCookieName      = "syllabuddy_token"
)

const balancedCSP = "default-src 'self'; " +
	"script-src 'self' https://accounts.google.com https://apis.google.com; " +
	"style-src 'self' 'unsafe-inline' https://accounts.google.com https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com data:; " +
	"img-src 'self' data: blob: https://*.googleusercontent.com; " +
	"connect-src 'self' https://accounts.google.com https://oauth2.googleapis.com https://www.googleapis.com; " +
	"frame-src 'self' https://accounts.google.com; " +
	"form-action 'self'; " +
	"base-uri 'self'; " +
	"frame-ancestors 'none'"

// HeadersMiddleware applies defensive HTTP security headers and balanced Google GSI CSP.
func HeadersMiddleware(isProduction bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
			h.Set("Content-Security-Policy", balancedCSP)

			if isProduction && (r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}

			next.ServeHTTP(w, r)
		})
	}
}

type ipBucket struct {
	tokens     float64
	lastUpdate time.Time
	lastSeen   time.Time
}

// RateLimiter enforces a bounded-memory per-IP token-bucket rate limit.
type RateLimiter struct {
	mu         sync.Mutex
	rps        float64
	burst      float64
	maxEntries int
	staleTTL   time.Duration
	buckets    map[string]*ipBucket
	ops        uint64
}

// NewRateLimiter constructs a per-IP token-bucket rate limiter with bounded map memory.
func NewRateLimiter(rps float64, burst int, maxEntries int) *RateLimiter {
	if rps <= 0 {
		rps = 10
	}
	if burst <= 0 {
		burst = 20
	}
	if maxEntries <= 0 {
		maxEntries = defaultMaxRateLimitEntries
	}
	return &RateLimiter{
		rps:        rps,
		burst:      float64(burst),
		maxEntries: maxEntries,
		staleTTL:   defaultBucketStaleTTL,
		buckets:    make(map[string]*ipBucket, 256),
	}
}

// Allow consumes one token for the given client IP, returning true if permitted.
func (rl *RateLimiter) Allow(ip string) bool {
	if ip == "" {
		ip = "unknown"
	}
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.ops++
	if rl.ops%256 == 0 || len(rl.buckets) >= rl.maxEntries {
		rl.pruneLocked(now)
	}

	b, exists := rl.buckets[ip]
	if !exists {
		if len(rl.buckets) >= rl.maxEntries {
			rl.evictOldestLocked()
		}
		rl.buckets[ip] = &ipBucket{
			tokens:     rl.burst - 1,
			lastUpdate: now,
			lastSeen:   now,
		}
		return true
	}

	elapsed := now.Sub(b.lastUpdate).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * rl.rps
		if b.tokens > rl.burst {
			b.tokens = rl.burst
		}
		b.lastUpdate = now
	}
	b.lastSeen = now

	if b.tokens < 1.0 {
		return false
	}
	b.tokens -= 1.0
	return true
}

func (rl *RateLimiter) pruneLocked(now time.Time) {
	for k, b := range rl.buckets {
		if now.Sub(b.lastSeen) > rl.staleTTL {
			delete(rl.buckets, k)
		}
	}
}

func (rl *RateLimiter) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time
	first := true
	for k, b := range rl.buckets {
		if first || b.lastSeen.Before(oldestTime) {
			oldestKey = k
			oldestTime = b.lastSeen
			first = false
		}
	}
	if oldestKey != "" {
		delete(rl.buckets, oldestKey)
	}
}

// Middleware rejects excess requests per IP with HTTP 429 Too Many Requests.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := logger.ClientIP(r)
		if !rl.Allow(ip) {
			w.Header().Set("Retry-After", "1")
			writeSecurityError(w, r, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRFMiddleware enforces same-origin checks on state-changing requests authenticated via cookies.
func CSRFMiddleware(appBaseURL, cookieName string) func(http.Handler) http.Handler {
	if cookieName == "" {
		cookieName = defaultAuthCookieName
	}
	configuredOrigin := normalizeOrigin(appBaseURL)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			// Only enforce CSRF check when the session auth cookie is present on the request.
			cookie, err := r.Cookie(cookieName)
			if err != nil || strings.TrimSpace(cookie.Value) == "" {
				next.ServeHTTP(w, r)
				return
			}

			secFetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
			if secFetchSite == "cross-site" {
				writeSecurityError(w, r, http.StatusForbidden, "cross-site request blocked")
				return
			}

			reqOrigin := requestSelfOrigin(r)
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
				if !originMatches(origin, configuredOrigin, reqOrigin) {
					writeSecurityError(w, r, http.StatusForbidden, "invalid request origin")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
				if !originMatches(referer, configuredOrigin, reqOrigin) {
					writeSecurityError(w, r, http.StatusForbidden, "invalid request referer")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if secFetchSite == "same-origin" {
				next.ServeHTTP(w, r)
				return
			}

			writeSecurityError(w, r, http.StatusForbidden, "missing origin header for cookie-authenticated mutation")
		})
	}
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func normalizeOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

func requestSelfOrigin(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(host)
}

func originMatches(candidate string, allowed ...string) bool {
	norm := normalizeOrigin(candidate)
	if norm == "" {
		return false
	}
	for _, target := range allowed {
		if target != "" && norm == target {
			return true
		}
	}
	return false
}

func writeSecurityError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
		return
	}
	http.Error(w, msg, status)
}
