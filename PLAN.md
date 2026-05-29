# Pacioli — Implementation Plan

## Status Legend
- ✅ Done
- 🚧 In progress
- ⬜ Not started
- 🔲 Deferred

---

## Phase 1 — Foundation ✅

- ✅ Go project structure (`cmd/server`, `internal/{account,security,transaction,distribution,fx,user,errs,sqlite}`)
- ✅ Per-concept packages: `account.Account`, `account.Store`, `security.Security`, etc.
- ✅ Store interfaces owned by each concept package (not shared repository layer)
- ✅ SQLite implementations with `modernc.org/sqlite` (no CGO)
- ✅ `shopspring/decimal` for all financial values (stored as TEXT in DB)
- ✅ `golang-migrate` with embedded SQL migrations
- ✅ `golangci-lint` + `gosec` + `govulncheck` via `make check`
- ✅ Pre-commit hook via `make install-hooks`
- ✅ Docker + `docker-compose.yml` (volume-persisted SQLite)
- ✅ Graceful shutdown, structured logging (`log/slog`)
- ✅ `README.md`, `CLAUDE.md`, `PLAN.md`

---

## Phase 2 — Manual Entry + Basic UI ✅

- ✅ Service layer (`internal/service/`) — wire stores, no HTTP coupling
- ✅ ACB calculation engine (average cost, non-registered accounts only)
- ✅ HTML templates (`web/templates/`) with HTMX + Pico.css
- ✅ HTTP handlers (`internal/handler/`)
- ✅ Account CRUD (create, list, edit, delete)
- ✅ Security search / add
- ✅ Manual transaction entry form (buy, sell, dividend, ROC adjustment, Norbert's Gambit)
- ✅ ACB display per security (current ACB, ACB/share, total shares)
- ✅ Basic nav / layout

---

## Phase 2.1 — Structured Logging ✅

- ✅ `responseWriter` wrapper — captures status code for request logging
- ✅ Request logging middleware — logs `method`, `path`, `status`, `latency`, `request_id` per request
- ✅ Context logger propagation — middleware seeds `slog.Logger` with `request_id`, stores in `context.Context` via typed key; `loggerFromCtx(ctx)` helper
- ✅ Handler methods use context logger — replace `h.logger` in request handlers with `loggerFromCtx(r.Context())`
- ✅ Wire middleware in `main.go` — wrap mux before passing to `http.Server`

---

## Phase 2.2 — Audit Log ✅

- ✅ `audit` package — `Entry`, `Action`, `EntityType`, `Source` types + `Store` interface
- ✅ Migration 000002 — `audit_log` table; `source` column on `accounts` and `securities`
- ✅ `sqlite.AuditStore` — writes to `audit_log`
- ✅ `source` field on `account.Account` and `security.Security`
- ✅ Handler `logAudit` helper — non-blocking; logs error on audit failure, never blocks primary op
- ✅ Audit entries on create/delete for accounts, securities, transactions
- ✅ JSON snapshot captured before delete — preserves full entity state for future recovery
- ✅ `user_id` from `h.userID` — swaps to session user when auth lands (Phase 6), no schema change

---

## Phase 3 — CSV Import ⬜

- ⬜ Broker profile system (pluggable column mappings per broker)
- ⬜ Canaccord CSV parser
- ⬜ Upload → preview → validate → commit flow
- ⬜ Flag unrecognized / ambiguous rows for manual review
- ⬜ Import history / audit log

---

## Phase 4 — Questrade Integration ⬜

- ⬜ Bank of Canada FX rate client (daily noon rates, cached in `fx_rates` table)
- ⬜ Questrade OAuth2 flow
- ⬜ Account + position sync
- ⬜ Transaction sync (activities endpoint)
- ⬜ Norbert's Gambit detection heuristic (auto-link paired DLR.TO / DLR.U.TO legs)
- ⬜ Manual FX rate override for historical entries

---

## Phase 5 — Reporting ⬜

- ⬜ Capital gains / losses by tax year
- ⬜ ACB history per security (full transaction log with running ACB)
- ⬜ Superficial loss detection + flagging (30-day window, non-registered only)
- ⬜ ROC adjustments from T3 distribution data
- ⬜ Export to CSV (for accountant / tax software)

---

## Phase 6 — Deferred 🔲

- 🔲 Options support
- 🔲 Corporate actions (splits, mergers, spin-offs)
- 🔲 Multi-user (users table already in schema; needs auth layer)
- 🔲 Additional broker CSV parsers

---

## Open Questions

| # | Question | Status |
|---|----------|--------|
| 1 | ROC distributions — confirm ETFs held that pay ROC (e.g. covered-call ETFs) | Pending user investigation |
| 2 | Canaccord CSV format — column names / date format to be confirmed on export | Pending export |
| 3 | Questrade API — confirm OAuth2 scopes needed for read-only activity sync | Phase 4 |
| 4 | Multi-user auth strategy (session cookie vs token) if open-sourced | Phase 6 |

---

## Key Constraints (CRA Rules)

- ACB pools across **all non-registered accounts** (margin + cash); registered accounts excluded
- **Average cost method** required (not FIFO/LIFO)
- Commissions: add to ACB on buy, deduct from proceeds on sell
- FX conversion fees: includable in ACB
- Superficial loss: sell at loss + buy back within 30 days (before or after) → loss denied, added to ACB
- Foreign securities: Bank of Canada noon rate authoritative; actual broker rate also acceptable