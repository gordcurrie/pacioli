# Pacioli — Copilot Instructions

Pacioli is a Canadian Adjusted Cost Base (ACB) tracker. Reviews must respect both Go conventions and CRA tax rules. Flag real correctness and security issues; do not flag style preferences or suggest abstractions beyond what the code requires.

## Stack

- **Go 1.26** — stdlib `net/http` only, no router framework
- **`shopspring/decimal`** — all financial values; `float32`/`float64` for money is always a bug
- **`modernc.org/sqlite`** — pure Go SQLite; missing rows return `sql.ErrNoRows`
- **`log/slog`** — stdlib structured logging only; no external logging library
- **HTMX + Pico.css** — minimal frontend; no JS frameworks

## What to flag

**Data integrity**
- Any financial calculation using `float32`/`float64` instead of `decimal.Decimal`
- Decimal values stored as anything other than `TEXT` in SQLite migrations
- ACB logic that doesn't use average cost method (Canada requires this, not FIFO/LIFO)
- ACB that goes negative after a Return of Capital adjustment (must floor at zero; excess ROC is a capital gain)
- Running share count or total ACB that isn't reset to zero on a full position sell
- Non-CAD security transactions submitted without an FX rate (silently records foreign currency amounts as CAD)

**Security**
- Any handler mutation (update, delete) that doesn't verify the resource belongs to `h.userID` before proceeding
- Missing `sql.ErrNoRows` → 404 mapping (unhandled errors should go through `notFoundOrError`, not `serverError`)
- Form inputs used without validation (security type, currency, transaction type must be whitelisted)

**Logging**
- Handler errors that use `h.logger` directly instead of `loggerFromCtx(r.Context())` — this drops the `request_id` from the error log line, breaking request correlation
- `h.logger` is only correct for startup/non-request contexts (template parse, `main()`)

**ACB domain rules**
- ACB pools only across non-registered accounts (Margin, Cash). Registered accounts (TFSA, RRSP, RESP, LRSP, SRSP) must be excluded from ACB calculations entirely
- Commissions: added to ACB on buy, deducted from proceeds on sell
- Superficial loss (sell at loss + repurchase within 30 days): flag only, do not auto-adjust ACB
- Norbert's Gambit legs must be linked via `linked_transaction_id`; neither leg alone is a taxable event

**Error handling**
- Errors must be wrapped: `fmt.Errorf("context: %w", err)`
- `sql.ErrNoRows` must map to HTTP 404 via `notFoundOrError`, not a 500

## What NOT to flag

- Missing comments — comments are intentionally omitted unless the WHY is non-obvious
- Single-use helpers or lack of abstraction — three similar lines is preferred over a premature abstraction
- Error handling for scenarios that cannot happen (internal invariants, framework guarantees)
- Absence of logging in service or store layers — errors bubble up to the handler layer by design
- `h.logger` usage inside `render()` — template errors are programming bugs, not request-scoped events
- Validation beyond what's needed at the current system boundary

## Conventions

- Store interfaces are named `Store` within their package: `account.Store`, not `account.AccountStore`
- SQLite implementations satisfy domain interfaces: `sqlite.AccountStore` satisfies `account.Store`
- Financial decimal values are stored as `TEXT` in SQLite to avoid float precision loss
- HTTP handlers live in `internal/handler/`; business logic in `internal/service/`; data access in `internal/sqlite/`
- Each concept package owns its own types and `Store` interface; no shared repository layer
