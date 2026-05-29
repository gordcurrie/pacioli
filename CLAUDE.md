# Pacioli — Claude Dev Guide

## Stack

- **Go 1.26** — backend, stdlib `net/http` (no router framework)
- **HTMX + Pico.css** — frontend (minimal JS)
- **SQLite** via `modernc.org/sqlite` (pure Go, no CGO)
- **`shopspring/decimal`** — all financial math; never use float for money
- **`golang-migrate`** — migrations embedded in `internal/sqlite/migrations/`
- **Docker + compose** — primary deployment target (Proxmox LXC)

## Architecture

Per-concept packages in `internal/`. Each owns type(s) + `Store` interface. Interface named `Store` within package — callers use `account.Store`, not `account.AccountStore`.

```
internal/
  account/      — Account, Type, Store interface
  security/     — Security, Type, Store interface
  transaction/  — Transaction, Type, Source, Store interface
  distribution/ — Distribution, Store interface
  user/         — User
  fx/           — FX Store interface
  errs/         — shared ErrNotFound
  sqlite/       — Store implementations + migration runner
  service/      — business logic (ACB engine, FX fetcher, etc.)  [Phase 2+]
  handler/      — HTTP handlers                                   [Phase 2+]
cmd/server/     — main.go, wires everything together
```

## Conventions

- `Store` suffix for data access interfaces (`AccountStore`, not `AccountRepository`)
- Concrete SQLite types match: `sqlite.AccountStore` satisfies `domain.AccountStore`
- Decimal strings in DB (TEXT columns) — avoids float precision loss
- Errors: wrap with `fmt.Errorf("context: %w", err)`; use `domain.ErrNotFound` for missing rows
- No comments unless WHY non-obvious

## Logging

- `log/slog` stdlib only — no external logging library
- `handler.RequestLogger(logger)` wraps the mux in `main.go`; logs `method`, `path`, `status`, `latency_ms`, `request_id` for every request
- Request-scoped logger stored in `context.Context` by the middleware; retrieve with `loggerFromCtx(r.Context())` inside handler package
- Handler errors use `loggerFromCtx(r.Context())` so `request_id` appears in error log lines, enabling correlation
- `h.logger` used only for startup/non-request contexts (template parse failures, `main()` fatal errors)
- Service and store layers do not log — errors bubble up to the handler layer

## Key Domain Rules (Canadian Tax)

- **ACB pools across all non-registered accounts** (margin + cash). Registered accounts (TFSA, RRSP, RESP, LRSP, SRSP) excluded from ACB entirely.
- **Average cost method** — Canada requires this, not FIFO/LIFO
- **Commissions** included in ACB on buy; deducted from proceeds on sell
- **FX conversion fees** includable in ACB cost
- **Norbert's Gambit** — two-leg currency conversion (e.g. DLR.TO → DLR.U.TO); stored as `fx_conversion` type with `linked_transaction_id` pairing legs
- **Return of Capital (ROC)** — annual T3 data; reduces ACB per unit held
- **Superficial loss** — sell at loss + repurchase within 30 days → flag, don't auto-adjust

## Make Targets

```bash
make build          # compile to bin/pacioli
make run            # go run ./cmd/server
make test           # go test -race ./...
make lint           # golangci-lint run ./...
make sec            # gosec -quiet ./...
make vuln           # govulncheck ./...
make check          # lint + sec + vuln (runs on pre-commit)
make install-hooks  # install git pre-commit hook
make tidy           # go mod tidy
```

## Environment Variables

| Var            | Default       | Purpose              |
|----------------|---------------|----------------------|
| `DATABASE_DSN` | `pacioli.db`  | SQLite file path     |
| `ADDR`         | `:8080`       | HTTP listen address  |

## Adding a Migration

1. Create `internal/sqlite/migrations/NNNNNN_description.up.sql` and `NNNNNN_description.down.sql`
2. Migrations run on startup via embedded iofs
