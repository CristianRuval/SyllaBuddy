# SyllaBuddy Deployment Guide — GCP Free Tier (`e2-micro`)

This guide walks through deploying the **SyllaBuddy** Go monolithic binary (with embedded pure-Go SQLite) and Caddy automatic HTTPS stack on a Google Cloud Platform **Always Free `e2-micro`** VM (2 shared vCPU, **1 GB RAM**, 30 GB standard persistent disk).

---

## 1. Architecture & Memory Budget

All UI templates, CSS, JS, and the SQLite database driver (`modernc.org/sqlite`) are embedded directly inside a single static Go binary (`CGO_ENABLED=0`), eliminating Node.js/Next.js, Python/FastAPI, and separate database containers.

| Service | Image / Build | Container Limit | Tuning Notes |
| --- | --- | --- | --- |
| `app` | Multi-stage `golang:1.26-alpine` → `alpine:3.21` + `poppler-utils` | `384m` | Embedded SQLite (WAL mode, 4 MB cache), `GOMEMLIMIT=300MiB`, `GOGC=75`, non-root `appuser` (`uid 10001`), `/healthz` check |
| `caddy` | `caddy:2-alpine` | `128m` | Automatic Let's Encrypt TLS for `syllabuddy.me`, gzip/zstd, `20MB` request limit |
| **Total** | | **512 MB** | Leaves ~512 MB for Linux kernel + Docker daemon (backed by 2 GB swap) |

> **Why single-origin Caddy?** Caddy proxies `https://syllabuddy.me` directly to `syllabuddy_app:8080`. Both the web pages and `/api/*` endpoints share the exact same origin, eliminating CORS preflight overhead and allowing `HttpOnly; Secure; SameSite=Lax` auth cookies.

---

## 2. GCP VM & DNS Prerequisites

1. **Create an `e2-micro` VM** in a GCP Free Tier-eligible US region (`us-west1`, `us-central1`, or `us-east1`) running **Ubuntu 24.04 LTS** or **Debian 12** with a **30 GB Standard Persistent Disk**.
2. **Firewall Rules** (VPC Network → Firewall / VM instance checkboxes):
   - Allow **HTTP** (`tcp:80`) and **HTTPS** (`tcp:443`) — required for Caddy ACME TLS certificate issuance and web traffic.
   - Optionally allow **HTTP/3 QUIC** (`udp:443`).
   - Do **not** expose port `8080` (Go app) publicly; Caddy communicates with `syllabuddy_app` over the internal Docker network.
3. **DNS**: Point an `A` record for `syllabuddy.me` to your VM's external IPv4 address. Wait for DNS propagation before starting Caddy so Let's Encrypt TLS provisioning succeeds immediately.

---

## 3. Swapfile Setup (Critical on 1 GB RAM)

Building the Go binary inside Docker and running concurrent `pdftoppm` thumbnail renders can briefly spike memory above 1 GB. Configure a **2 GB swapfile** before installing or building anything:

```bash
# 1. Create a 2 GB swapfile
sudo fallocate -l 2G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile

# 2. Persist across reboots
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab

# 3. Prefer RAM and only swap under pressure
echo 'vm.swappiness=10' | sudo tee /etc/sysctl.d/99-syllabuddy.conf
sudo sysctl -p /etc/sysctl.d/99-syllabuddy.conf

# 4. Verify
free -h
swapon --show
```

---

## 4. Install Docker & Docker Compose

On Ubuntu/Debian:

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl gnupg openssl

# Install Docker Engine + Compose v2 plugin via official script
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker "$USER"
newgrp docker

docker --version
docker compose version
```

---

## 5. Configure Environment & Permissions

1. Clone or copy the repository onto the VM and `cd` into the project directory:

   ```bash
   cd ~/SyllabuddyProd
   ```

2. Create `.env` from the safe template:

   ```bash
   cp .env.example .env
   chmod 600 .env
   ```

3. Generate a strong secret for `SECRET_KEY`:

   ```bash
   openssl rand -hex 32   # use for SECRET_KEY
   ```

4. Edit `.env` (`nano .env`) and set:
   - `SECRET_KEY` — generated 64-char hex string
   - `GEMINI_API_KEY` — your Google AI Studio / Gemini API key
   - `GOOGLE_CLIENT_ID` — your Google OAuth 2.0 Web Client ID (optional; omit to disable Google Sign-In/Calendar buttons)
   - `BETA_INVITE_CODE` — set a shared invite code to gate signup, or leave empty (`BETA_INVITE_CODE=`) for open public beta signup
   - `APP_BASE_URL=https://syllabuddy.me`

5. Create the uploads and SQLite data directories with ownership matching the container's non-root `appuser` (`uid 10001`, `gid 10001`):

   ```bash
   mkdir -p uploads_data sqlite_data
   sudo chown -R 10001:10001 uploads_data sqlite_data
   ```

---

## 6. Build & Run with Docker Compose

1. Validate the Compose file and build/start the stack:

   ```bash
   docker compose config -q
   docker compose up -d --build
   ```

   > **Tip:** The first Go compile on an `e2-micro` takes ~1–3 minutes. Keep `docker compose up -d --build` running; the 2 GB swapfile prevents OOM kills during `go build`.

2. Verify container status and healthchecks:

   ```bash
   docker compose ps
   docker compose logs -f --tail=50
   ```

   Wait until `syllabuddy_app` reports `healthy` and `syllabuddy_caddy` obtains a TLS certificate for `syllabuddy.me`.

3. Smoke-test the live deployment:

   ```bash
   curl -fsS https://syllabuddy.me/healthz
   ```

4. **Routine Operations**:
   - **Update after code changes**:
     ```bash
     git pull
     docker compose up -d --build
     docker image prune -f
     ```
   - **Database backup (SQLite online backup)**:
     ```bash
     cp sqlite_data/syllabuddy.db "backup_$(date +%F).db"
     ```
   - **Stop stack**:
     ```bash
     docker compose down
     ```
     *(Volumes `caddy_data`, `caddy_config`, `./sqlite_data`, and `./uploads_data` are preserved.)*

---

## 7. Google Cloud OAuth Consent Screen & Legal Checklist

To enable **Google Sign-In** and **Sync to Google Calendar** in production without user-facing errors:

1. **OAuth 2.0 Client ID (`APIs & Services → Credentials`)**:
   - Application type: **Web application**.
   - **Authorized JavaScript origins**: `https://syllabuddy.me` (add `http://localhost:8080` only in local dev).
   - **Authorized redirect URIs**: `https://syllabuddy.me` (if using Google Identity Services popup/token flow, ensure the origin matches `APP_BASE_URL`).
   - Copy the Client ID into `GOOGLE_CLIENT_ID` in `.env`.

2. **COOP Header Note (`Caddyfile` & Go middleware)**:
   - Both Caddy and the Go security middleware send `Cross-Origin-Opener-Policy: same-origin-allow-popups`, which allows Google Identity Services (GIS) popups and One Tap to communicate back via `postMessage` while maintaining origin isolation.
   - If you observe GIS popup closure/communication errors in a specific browser, test switching `Cross-Origin-Opener-Policy` in `Caddyfile` (and Go middleware) to `unsafe-none` and reload Caddy (`docker compose exec caddy caddy reload --config /etc/caddy/Caddyfile`).

3. **OAuth Consent Screen (`APIs & Services → OAuth consent screen`)**:
   - **App name**: `SyllaBuddy`
   - **User support email** and **Developer contact email**: active monitored addresses.
   - **App domain links** (must use your verified domain `syllabuddy.me`):
     - Application home page: `https://syllabuddy.me`
     - Privacy Policy link: `https://syllabuddy.me/privacy`
     - Terms of Service link: `https://syllabuddy.me/terms`
   - **Scopes**: Request the minimum required scopes (`openid`, `email`, `profile`, and `https://www.googleapis.com/auth/calendar.events` / `https://www.googleapis.com/auth/calendar` as configured).

4. **Testing vs. In Production Publishing**:
   - **Testing mode**: Hard-capped at **100 manually added test users**, and Google refresh tokens expire every **7 days**. Suitable only for private closed testing.
   - **In Production mode**: Removes the 100-user cap and 7-day expiry. Note that sensitive/restricted scopes (such as Google Calendar) display an **"Unverified app"** warning screen until Google completes OAuth App Verification (requiring domain verification in Google Search Console, live `/privacy` and `/terms` pages, scope justification, and a YouTube demo video of the OAuth flow).

5. **Google API Services User Data Policy (Limited Use Disclosure)**:
   - Ensure `/privacy` (and your OAuth verification submission) explicitly includes the required disclosure:
     > *"SyllaBuddy's use and transfer of information received from Google APIs to any other app will adhere to the [Google API Services User Data Policy](https://developers.google.com/terms/api-services-user-data-policy), including the Limited Use requirements."*

6. **Additional Production Legal Pages**:
   - `/privacy` covers uploaded PDF retention, Gemini AI processing, essential session cookie/`localStorage` theme preference usage, and self-service account/data deletion.
   - `/terms` covers the beta "as-is" disclaimer, AI extraction accuracy disclaimer (students must verify due dates against official syllabi), and user representation of lawful access to uploaded academic syllabi.

---

## 8. Gemini API Privacy Note (Paid vs. Free Tier)

- **Gemini API Free Tier**: Under Google's Free Tier terms, prompts and uploaded PDF syllabi may be logged and used by Google to improve products/models, including potential human review.
- **Gemini API Paid Tier (Cloud Billing enabled)**: Google **does not** use your prompts or uploaded PDFs to train models, and logging is restricted to abuse/legal compliance.
- **Recommendation**: For any public or student beta deployment, attach a Cloud Billing account to your Google AI Studio / Gemini API project so all syllabus PDFs are processed under **Paid Tier** privacy terms, and avoid uploading documents containing sensitive personal information (SSNs, grades, financial aid details).
