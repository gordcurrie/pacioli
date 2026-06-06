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
- ✅ Audit entries on create/delete for accounts and transactions; create-only for securities (no delete handler yet)
- ✅ JSON snapshot captured before delete — preserves full entity state for future recovery
- ✅ `user_id` from `h.userID` — swaps to session user when auth lands (Phase 6), no schema change

---

## Phase 3 — CSV Import ✅

- ✅ `internal/broker` package — `Profile` interface + `ParseCSV` function; add new brokers by implementing one interface
- ✅ Canaccord Genuity parser — date format, column mapping, type classification, commission derivation, security name extraction
- ✅ Upload → preview → commit flow: multipart upload → resolve accounts/securities → hidden JSON commit payload → create transactions
- ✅ Flagged rows excluded from commit: unknown account, unmatched security, unrecognized type, fund exchanges, registered-account transfers
- ✅ Skipped rows: fees, GST, withholding tax, notional distributions, zero-amount ROC (T3 data — enter manually)
- ✅ Import batch tracking via `import_id` in `audit_log` — all committed rows share one hex-encoded 16-byte random ID per import
- ✅ `import/` directory in `.gitignore` — sample CSVs never committed

---

## Phase 4 — Questrade Integration ✅

- ✅ Bank of Canada FX rate client (daily noon rates, cached in `fx_rates` table)
- ✅ Questrade OAuth2 flow (connect/disconnect, AES-256-GCM token encryption at rest)
- ✅ Account + activity sync (activities endpoint with 30-day chunking)
- ✅ Transaction sync — buy/sell/dividend/REI imported; FXT flagged for Norbert's Gambit review
- ⬜ Norbert's Gambit detection heuristic (auto-link paired DLR.TO / DLR.U.TO legs)
- ⬜ Manual FX rate override for historical entries

---

## Phase 4.1 — Data Management ✅

- ✅ Security edit + delete (currently create-only)
- ✅ Transaction FX rate edit — inline HTMX override of BoC rate on existing transactions; recalculates priceCAD/commCAD
- ✅ Questrade "Sync accounts & securities" — POST `/questrade/sync`; fetches positions across all accounts, creates missing accounts (number/type/broker) and securities (ticker/currency); uses Questrade symbol search API to fill name/exchange/type
- ✅ Security form symbol lookup — type a ticker, Questrade symbol search pre-fills exchange/name/type/currency via HTMX OOB swaps

---

## Phase 5 — Reporting ✅

- ✅ Capital gains / losses by tax year
- ✅ ACB history per security (full transaction log with running ACB)
- ✅ Superficial loss detection + flagging (30-day window, all accounts per CRA affiliated-person rule)
- ✅ ROC adjustments from T3 distribution data (preview + apply flow)
- ✅ Export to CSV (for accountant / tax software)

## Phase 5.1 — Disposition Detail Drill-Down ✅

- ✅ `GET /gains/{year}/{security_id}` — full ACB history up to last disposal in year
- ✅ Security ticker on gains report links to detail page
- ✅ Disposal rows highlighted; gain/loss shown inline on disposal rows only
- ✅ `GainsService.HistoryForSecurity` — fetches, computes, and trims history to last disposal
- ✅ `gains_detail.html` — Date | Type | Qty | Price | Commission | Shares After | Running ACB | ACB/Share | Gain/Loss

---

## Phase 6 — Multi-User Auth + TOTP 2FA ✅

- ✅ Migration 000005 — extend `users` table (password_hash, is_admin, totp_secret, totp_enabled); add `sessions` + `recovery_codes` tables
- ✅ Session-based auth — 30-day sessions; raw token in cookie, SHA-256 hash in DB; HttpOnly + SameSite=Strict; configurable `Secure` via `SECURE_COOKIES` env
- ✅ First-run setup flow — `/setup` locked after first configured user; upserts existing unconfigured user to preserve data
- ✅ Login / logout — bcrypt cost 12; `/login` and `/logout`
- ✅ TOTP 2FA — `pquerna/otp` (RFC 6238); QR code rendered as `data:image/png;base64` (no external service); 10 single-use recovery codes; encrypted at rest via `TOKEN_ENCRYPTION_KEY`
- ✅ Admin-managed user creation — `/admin/users`; admin can reset passwords
- ✅ Profile — password change (`/profile/password`), 2FA enable/disable (`/profile/2fa`)
- ✅ Auth middleware — `RequireAuth`, `RequireAdmin`, `SetupGate`; all existing routes protected
- ✅ `h.userID` removed — user comes from request context set by `RequireAuth`

## Phase 7 — ACB Correctness & Completeness ⬜

Close the gaps that can produce wrong ACB numbers before building reporting on top of them.

- ⬜ Superficial loss ACB carry-forward — currently flagged only; adjust ACB of replacement shares per CRA rule (denied loss added to replacement position's ACB)
- ⬜ Stock splits / consolidations — new-shares-for-old at zero cost; adjusts ACB/share and share count; needed for any position that has split
- ⬜ Stock dividends — shares issued in lieu of cash dividend; FMV of shares received is income, same FMV becomes ACB of new shares
- ⬜ Phantom distributions — reinvested distributions that increase ACB without a cash inflow (common in ETFs); T3 box 42 data
- ⬜ Norbert's Gambit auto-detection — heuristic to auto-link paired DLR.TO / DLR.U.TO legs (deferred from Phase 4)
- ⬜ Manual FX rate override for historical entries (deferred from Phase 4)

---

## Phase 8 — RSU / ESPP Support ⬜

E*Trade-specific workflow; none of this comes from any Canadian broker automatically.

- ⬜ RSU vest transaction type — FMV at vest date × BoC noon rate = CAD ACB lot; each vest is a separate cost lot; employment income reported separately from capital gain on sale
- ⬜ ESPP discount tracking — discount at purchase is employment income, not capital gain; ACB = full FMV at purchase date (not discounted price); needs separate flag/split
- ⬜ Options exercise — exercise price becomes ACB; difference between FMV and exercise price may be employment income (employee options) vs capital (warrants); flag type at entry
- ⬜ E*Trade CSV parser — map E*Trade activity export columns to Pacioli transaction types; handle USD-denominated entries with FX conversion

---

## Phase 9 — Tax Reporting ⬜

Produce outputs that are directly useful at tax time.

- ⬜ Schedule 3 export — capital gains summary formatted for CRA Schedule 3; one row per disposition, proceeds / ACB / gain-loss / superficial loss adjustment
- ⬜ Annual tax summary — realized gains by year broken down by eligible vs non-eligible dividends, capital gains, return of capital; printable/exportable
- ⬜ T3 / T5 slip import — formal structured import for annual slip data (ROC, eligible dividends, foreign income); links to security + tax year
- ⬜ T5008 reconciliation — match broker-issued T5008 slips to Pacioli ACB records; flag discrepancies so you know what to correct before filing
- ⬜ T1135 threshold alert — foreign property >$100k CAD triggers CRA T1135 filing requirement; dashboard warning when E*Trade (or any foreign) holdings approach/exceed threshold

---

## Phase 10 — Portfolio Dashboard ⬜

Day-to-day visibility into positions and unrealized P&L.

- ⬜ Current positions view — shares held per security per account, aggregated across accounts; sourced from transaction history not broker API
- ⬜ Unrealized gains estimate — current position × last known price vs ACB; gain/loss in CAD; requires price feed or manual price entry
- ⬜ Multi-currency P&L breakdown — USD vs CAD unrealized gain; FX gain/loss component separated from position gain/loss
- ⬜ Portfolio allocation summary — by account type (registered vs non-registered), by currency, by asset class

---

## Phase 11 — Additional Brokers & Corporate Actions ⬜

- ⬜ Canaccord CSV parser improvements — refine based on actual transfer history export
- ⬜ Additional broker CSV parsers (IBKR, Wealthsimple, etc.)
- ⬜ Corporate actions — mergers, spin-offs, return of capital at corporate level; ACB allocation across old/new positions
- ⬜ Share class conversions (e.g. dual-class restructuring)

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