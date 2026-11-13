# SyllaBuddy — AI Syllabus Tracking & Calendar Workspace

**SyllaBuddy** is a lightweight, single-binary Go web application that turns academic syllabus PDFs into an editable semester timeline, universal `.ics` calendar feeds, and deduplicated Google Calendar events.

Originally built as a Python (FastAPI) + Next.js + PostgreSQL stack, SyllaBuddy was re-architected into a **pure-Go monolithic server with embedded SQLite and server-rendered templates** so the entire application runs smoothly on a **Google Cloud Platform Always Free `e2-micro` VM (1 GB RAM)** inside Docker with automatic HTTPS via Caddy.

---

## Key Features

- **Gemini AI PDF Extraction:** Asynchronous 2-worker background queue (`gemini-2.5-flash`) extracts course titles, course codes, instructors, exams, assignments, and holidays from uploaded syllabus PDFs, with clear error reporting and one-click re-parsing.
- **PDF Thumbnails & In-App Viewer:** Generates first-page PNG thumbnails (`poppler-utils` / `pdftoppm`) and serves uploaded PDFs securely with strict per-user ownership checks and path-traversal validation.
- **Unified Calendar & Inline Date Editing:** Filter deadlines across courses and event types (`exam`, `assignment`, `holiday`, `other`), search by keyword, and update shifted due dates inline.
- **Universal `.ics` Export & Deduplicated Google Calendar Sync:**
  - Download RFC 5545 `.ics` files per course or for your entire semester.
  - Push events directly to Google Calendar via OAuth (`calendar.events`) using Google Calendar's `events/import` endpoint and deterministic `iCalUID`s (`sb-<sha256>@syllabuddy.me`) so repeated syncs and inline date edits update in place without duplicates.
- **Modern Responsive Light / Dark UI:** White/blue light mode and black/blue dark mode with `localStorage` persistence, built with zero-bundler vanilla JS/CSS and embedded directly into the Go binary via `go:embed`.

---

## Architecture & Engineering Highlights

| Layer | Choice | Why |
| :--- | :--- | :--- |
| **Backend & SSR** | Go 1.26 (`net/http`, `html/template`, `embed`) | Single static `CGO_ENABLED=0` binary (~18 MB), low idle RAM, zero CORS overhead |
| **Database** | Embedded SQLite (`modernc.org/sqlite`, WAL mode) | Zero external DB container, pure Go, fast local reads/writes on 1 GB RAM |
| **AI Parser** | `google.golang.org/genai` (`gemini-2.5-flash`) | Native PDF multimodal extraction with bounded timeouts and sanitized error handling |
| **Reverse Proxy** | Caddy 2 (`caddy:2-alpine`) | Automatic Let's Encrypt TLS, gzip/zstd compression, and edge security headers |
| **Security** | JWT (`HttpOnly`, `SameSite=Lax`), `bcrypt`, Google Sign-In, CSRF, rate limiting | Origin/Referer CSRF validation, token-bucket rate limiters, CSP/HSTS/COOP headers |
| **Observability** | Structured JSON logging (`log/slog`) | Request IDs, latency tracking, and secret-safe `LogValuer` config redaction |

### Resource Budget (GCP `e2-micro`)
- `syllabuddy_app`: `384m` memory limit (`GOMEMLIMIT=300MiB`, `GOGC=75`), non-root `alpine:3.21` user (`uid 10001`).
- `syllabuddy_caddy`: `128m` memory limit for TLS termination and reverse proxying.

---

## Project Structure

```text
├── cmd/syllabuddy/        # Application entrypoint, DB migration, graceful shutdown
├── internal/
│   ├── auth/              # JWT cookies, bcrypt hashing, Google ID token verification
│   ├── config/            # Environment loading and secret-safe LogValuer
│   ├── db/                # SQLite connection, schema migrations, strict user-scoped queries
│   ├── gemini/            # Gemini PDF extraction prompt, client, JSON normalization
│   ├── handlers/          # HTTP routes, SSR pages, upload/reparse queue, ICS & Google sync
│   ├── ics/               # RFC 5545 .ics calendar generator
│   ├── logger/            # Structured slog middleware + request IDs + panic recovery
│   ├── models/            # Domain structs (User, Syllabus, ParserOutput, SyllabusEvent)
│   ├── pdfutil/           # PDF validation and pdftoppm first-page thumbnail rendering
│   └── security/          # Security headers, CSRF middleware, IP rate limiters
├── web/
│   ├── static/            # Embedded CSS, JS, and SVG favicon
│   └── templates/         # Embedded Go HTML templates (landing, login, dashboard, legal)
├── Caddyfile              # Automatic HTTPS reverse proxy config
├── Dockerfile             # Multi-stage static Go build -> minimal Alpine runtime
├── docker-compose.yml     # Production stack (app + Caddy)
└── docker-compose.local.yml # Local HTTP test override (http://localhost:8080)
```

---

## Quick Start

### Local Test (Docker Desktop)
```bash
cp .env.example .env
# Set SECRET_KEY (e.g. openssl rand -hex 32) and GEMINI_API_KEY in .env
docker compose -f docker-compose.yml -f docker-compose.local.yml up -d --build app
```
Open `http://localhost:8080`.

### Production Deployment (GCP `e2-micro` + Caddy HTTPS)
See [DEPLOYMENT.md](DEPLOYMENT.md) for swapfile setup, DNS/firewall configuration, non-root volume permissions (`10001:10001`), and Google OAuth Consent Screen verification steps.

---

## Running Tests
```bash
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test -count=1 ./...
```
