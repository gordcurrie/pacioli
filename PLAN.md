# Pacioli — Refactoring Plan

## Duplication

- [ ] **1. Config.validate() — 13 identical switch cases** (`handler/handler.go:75-106`)
  Every branch is `case cfg.X == nil: return fmt.Errorf("handler: X is required")`. Table-driven loop over `[]struct{val any; name string}` shrinks to ~10 lines.

- [ ] **2. Decimal parse pattern repeated 3x** (`handler/transaction.go:150-163`)
  `quantity`, `price`, `commission` each parsed identically — only field name differs. Extract `parseDecimal(r, field) (decimal.Decimal, error)` helper.

- [x] **3. editTransactionFXForm / transactionFXCell duplicate fetch** (`handler/transaction.go:260-302`)
  Both fetch the transaction, fetch the account, check ownership. Same 20-line block. Extract `fetchAndOwnTx(r) (*transaction.Transaction, *account.Account, error)`.

- [x] **4. createSecurity renders error form 3x with same struct literal** (`handler/security.go:273-305`)
  Three identical `securityFormData{Security: &security.Security{Currency: "CAD"}, Types: securityTypes, Error: "..."}` constructions. Extract `h.renderSecurityForm(w, r, securityTypes, errMsg string)`.

- [x] **5. PRAGMA busy_timeout set twice** (`sqlite/db.go:34,41`)
  Set in both DSN string and via `ExecContext`. Pick one.

- [ ] **6. Admin handlers repeat `h.users.List()` + render** (`handler/admin.go:36,79,94,107,245,280`)
  Extract `renderAdminUsers(w, r, errMsg string)` helper that fetches + renders.

---

## Unnecessary Complexity

- [x] **7. questradePreview renderErr closure** (`handler/questrade.go:269-279`)
  Closure called 6+ times; rebuilds accounts/options slice on every invocation. Capture once before closure.

- [ ] **8. userOwnsSecurityID called twice per ACB handler flow** (`handler/acb.go:80-86,95,158`)
  Fetches all security IDs for user each call. Load once, pass result.

- [ ] **9. totpSetupPageData multiple overlapping render paths** (`handler/profile.go:216-270`)
  Extract into single `h.renderTOTPSetup(w, r, data totpSetupPageData)`.

---

## Structural

- [ ] **10. accountScanner interface** (`sqlite/account.go:92-94`)
  Wraps `sql.Row`/`sql.Rows` for single use in `scanAccount`. Accept `interface { Scan(...any) error }` directly, or split into two typed functions.

- [x] **11. NGSvc optional nil-check** (`handler/handler.go:229-232`)
  Only optional service; nil-guard in `Routes` silently disables feature. Add comment explaining this is intentional.
