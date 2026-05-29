---
applyTo: "**/*.sql"
---

Pacioli uses `golang-migrate` with embedded SQL files in `internal/sqlite/migrations/`.

## What to flag

- Decimal/financial columns stored as `REAL`, `FLOAT`, or `NUMERIC` — they must be `TEXT` to avoid float precision loss; `shopspring/decimal` serialises to/from string
- Missing paired `.down.sql` for every `.up.sql`
- Migrations that modify existing columns without a down migration that reverses the change
- `NOT NULL` columns added to existing tables without a `DEFAULT` (will fail on non-empty databases)
- ACB-relevant tables missing `user_id` scoping — all financial data is per-user

## Conventions

- Migration files named `NNNNNN_description.up.sql` / `NNNNNN_description.down.sql` with zero-padded sequential number
- Registered account types (TFSA, RRSP, RESP, LRSP, SRSP) must remain distinguishable from non-registered types (Margin, Cash) — ACB exclusion depends on this
