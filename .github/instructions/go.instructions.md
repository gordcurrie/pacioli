---
applyTo: "**/*.go"
---

Pacioli is a Canadian Adjusted Cost Base (ACB) tracker. Flag real correctness and security issues; do not flag style preferences or suggest abstractions beyond what the code requires.

## What to flag

**Data integrity**
- Any financial calculation using `float32`/`float64` instead of `decimal.Decimal` — this is always a bug
- ACB logic that doesn't use average cost method (Canada requires this, not FIFO/LIFO)
- ACB that goes negative after a Return of Capital adjustment (must floor at zero; excess ROC is a capital gain, not a negative ACB)
- Running share count or total ACB not reset to zero on a full position sell
- Non-CAD security transactions submitted without an FX rate (silently records foreign currency amounts as CAD, corrupting ACB)

**Security**
- Any handler mutation (update, delete) that doesn't verify the resource belongs to `h.userID` before proceeding
- Missing `sql.ErrNoRows` → 404 mapping — use `notFoundOrError`, not `serverError`
- Form inputs used without whitelisting (security type, currency, transaction type must be validated against known-good lists)

**Logging**
- Handler errors using `h.logger` directly instead of `loggerFromCtx(r.Context())` — this drops `request_id` from error log lines, breaking request correlation
- `h.logger` is only correct for startup/non-request contexts (template parse failures, `main()`)

**ACB domain rules**
- ACB pools only across non-registered accounts (Margin, Cash). Registered accounts (TFSA, RRSP, RESP, LRSP, SRSP) must be excluded from ACB calculations entirely
- Commissions: added to ACB on buy, deducted from proceeds on sell — not the other way around
- Superficial loss (sell at loss + repurchase within 30 days): flag only, do not auto-adjust ACB
- Norbert's Gambit legs must be linked via `linked_transaction_id`; neither leg alone is a taxable event

**Error handling**
- Errors must be wrapped: `fmt.Errorf("context: %w", err)`
- `sql.ErrNoRows` must map to HTTP 404 via `notFoundOrError`, not 500

## What NOT to flag

- Missing comments — intentionally omitted unless the WHY is non-obvious
- Lack of abstraction — three similar lines is preferred over a premature helper
- Error handling for scenarios that cannot happen (internal invariants, framework guarantees)
- Absence of logging in service or store layers — errors bubble up to the handler layer by design
- `h.logger` usage inside `render()` — template errors are programming bugs, not request-scoped events

## Conventions

- Store interfaces named `Store` within their package: `account.Store`, not `account.AccountStore`
- `sqlite.AccountStore` satisfies `account.Store` — concrete type in `internal/sqlite/`, interface in domain package
- HTTP handlers in `internal/handler/`; business logic in `internal/service/`; data access in `internal/sqlite/`
- Each concept package owns its own types and `Store` interface; no shared repository layer
- `shopspring/decimal` for all financial values; never `float32`/`float64` for money
