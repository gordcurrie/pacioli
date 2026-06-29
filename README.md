# Pacioli

Adjusted Cost Base (ACB) tracker for Canadian investors. Multi-user web app with Questrade API sync, Canaccord CSV import, and CRA-compliant capital gains reporting.

Named after [Luca Pacioli](https://en.wikipedia.org/wiki/Luca_Pacioli), the father of double-entry bookkeeping.

## Features

**ACB & Tax**
- ACB tracking per security across all non-registered accounts (CRA average-cost method)
- CAD and native-currency cost tracking with BoC exchange rates
- Commission and FX conversion fee inclusion in ACB
- Norbert's Gambit auto-detection and transaction pairing (journal and direct-path)
- Return of Capital ACB reductions from T3 distribution data
- Superficial loss detection with carry-forward adjustments per CRA rules
- Capital gains report by tax year with CSV export

**Data Import**
- Questrade API sync (margin, cash, TFSA, RRSP, RESP, LRSP, SRSP accounts)
- Canaccord Genuity activity CSV import
- Manual transaction entry

**Auth & Admin**
- Multi-user with email/password login
- TOTP two-factor authentication with recovery codes
- Admin: user management, audit log viewer

## Setup

### Prerequisites

- Go 1.26+
- `golangci-lint` — `brew install golangci-lint`
- `gosec` — `go install github.com/securego/gosec/v2/cmd/gosec@latest`
- `govulncheck` — `go install golang.org/x/vuln/cmd/govulncheck@latest`
- `air` — `go install github.com/air-verse/air@latest` (hot reload for local dev)

### Local development

```bash
git clone https://github.com/gordcurrie/pacioli
cd pacioli
go mod download
make install-hooks   # installs pre-commit lint/sec/vuln checks
```

Set up environment variables (uses [direnv](https://direnv.net/)):

```bash
cp .envrc.example .envrc
# TOKEN_ENCRYPTION_KEY is required for Questrade import and TOTP 2FA.
# Generate: openssl rand -hex 32
direnv allow
```

Start the server:

```bash
make dev   # hot reload via air
```

Server starts at `http://localhost:8080`. On first visit, a setup wizard creates the admin user.

### Docker

Generate your encryption key once and save it — rotating it makes existing Questrade tokens and TOTP secrets unreadable:

```bash
[ -f .env ] || echo "TOKEN_ENCRYPTION_KEY=$(openssl rand -hex 32)" > .env
docker compose up --build
```

Without `TOKEN_ENCRYPTION_KEY` set, Questrade import and TOTP 2FA are unavailable.

Data persists in the `pacioli-data` Docker volume.

## Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_DSN` | `pacioli.db` | SQLite file path |
| `ADDR` | `:8080` | HTTP listen address |
| `TOKEN_ENCRYPTION_KEY` | *(unset)* | 64 hex chars (32 bytes) — AES-256-GCM key for Questrade tokens and TOTP secrets. Required for Questrade import and 2FA. Generate: `openssl rand -hex 32` |
| `SECURE_COOKIES` | `false` | Set `true` in production (HTTPS only) |

## Production

Run behind a TLS-terminating reverse proxy (nginx, Caddy, Traefik). Set `SECURE_COOKIES=true` so session cookies are marked `Secure`.

Minimal nginx snippet:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
}
```

**Data**

The SQLite database is a single file. Back it up by copying it while pacioli is stopped, or use `.backup` via the SQLite CLI for a hot copy:

```bash
sqlite3 pacioli.db ".backup pacioli.db.bak"
```

With Docker, the database lives in the `pacioli-data` volume. Back it up by mounting the volume into a throwaway container:

```bash
docker run --rm -v pacioli-data:/data -v $(pwd):/out alpine \
  cp /data/pacioli.db /out/pacioli.db.bak
```

**First run**

On first visit the setup wizard prompts for an admin email, password, and optionally a second admin. After setup is complete, the wizard is permanently disabled (no `FIRST_RUN` flag needed).

## Development

```bash
make check    # build + test + lint + gosec + govulncheck (also runs on pre-commit)
make test     # go test -race ./...
make build    # compile to bin/pacioli
make run      # go run ./cmd/server
make tidy     # go mod tidy
```

### Logs

Structured logs via stdlib `slog`. Every request emits one line:

```
time=... level=INFO msg=request method=GET path=/accounts status=200 latency_ms=3 request_id=3f2a8b1c4e5d6f7a
```

Handler errors include the same `request_id` for correlation. Service and store layers do not log — errors bubble to the handler.

### Architecture

Per-concept packages in `internal/`. Each owns its domain type(s) and a `Store` interface. SQLite implementations live in `internal/sqlite/`. Business logic (ACB engine, gains calculator, NG detector, ROC service, BoC FX fetcher) lives in `internal/service/`.

See [CLAUDE.md](CLAUDE.md) for full conventions, Canadian tax domain rules, and architecture decisions.

## License

MIT
