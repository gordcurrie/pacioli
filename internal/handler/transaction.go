package handler

import (
	"cmp"
	"encoding/json"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type transactionRow struct {
	*transaction.Transaction
	Ticker      string
	AccountName string
}

type transactionsPageData struct {
	Transactions []transactionRow
	Error        string
}

type transactionFormData struct {
	Accounts []*account.Account
	Types    []transaction.Type
	Error    string
}

var txTypes = []transaction.Type{
	transaction.TypeBuy, transaction.TypeSell, transaction.TypeDividend,
	transaction.TypeROCAdjustment, transaction.TypeFXConversion,
	transaction.TypeTransferIn, transaction.TypeTransferOut, transaction.TypeJournal,
}

func (h *Handler) listTransactions(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.accounts.ListByUser(r.Context(), userFromCtx(r.Context()).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	accountNames := make(map[int64]string, len(accounts))
	for _, a := range accounts {
		accountNames[a.ID] = a.Name
	}

	securities, err := h.securities.ListAll(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	tickers := make(map[int64]string, len(securities))
	for _, s := range securities {
		tickers[s.ID] = s.Ticker
	}

	var raw []*transaction.Transaction
	for _, a := range accounts {
		atxs, err := h.transactions.ListByAccount(r.Context(), a.ID)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		raw = append(raw, atxs...)
	}

	slices.SortFunc(raw, func(a, b *transaction.Transaction) int {
		if c := cmp.Compare(a.TradeDate.Unix(), b.TradeDate.Unix()); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})

	rows := make([]transactionRow, len(raw))
	for i, tx := range raw {
		rows[i] = transactionRow{
			Transaction: tx,
			Ticker:      tickers[tx.SecurityID],
			AccountName: accountNames[tx.AccountID],
		}
	}

	h.render(w, r,"transactions", transactionsPageData{Transactions: rows})
}

func (h *Handler) newTransaction(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.accounts.ListByUser(r.Context(), userFromCtx(r.Context()).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r,"transaction_form", transactionFormData{
		Accounts: accounts,
		Types:    txTypes,
	})
}

func (h *Handler) createTransaction(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.accounts.ListByUser(r.Context(), userFromCtx(r.Context()).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	renderForm := func(errMsg string) {
		h.render(w, r,"transaction_form", transactionFormData{
			Accounts: accounts,
			Types:    txTypes,
			Error:    errMsg,
		})
	}

	if err := r.ParseForm(); err != nil {
		renderForm("invalid form data")
		return
	}

	accountID, err := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if err != nil || accountID == 0 {
		renderForm("select an account")
		return
	}
	var ownsAccount bool
	for _, a := range accounts {
		if a.ID == accountID {
			ownsAccount = true
			break
		}
	}
	if !ownsAccount {
		renderForm("invalid account")
		return
	}

	securityID, err := strconv.ParseInt(r.FormValue("security_id"), 10, 64)
	if err != nil || securityID == 0 {
		renderForm("select a security")
		return
	}
	sec, err := h.securities.GetByID(r.Context(), securityID)
	if err != nil {
		renderForm("security not found")
		return
	}

	qty, err := decimal.NewFromString(r.FormValue("quantity"))
	if err != nil || !qty.IsPositive() {
		renderForm("quantity must be greater than zero")
		return
	}
	priceNative, err := decimal.NewFromString(r.FormValue("price_native"))
	if err != nil || priceNative.IsNegative() {
		renderForm("price must be zero or greater")
		return
	}
	commNative, err := decimal.NewFromString(r.FormValue("commission_native"))
	if err != nil || commNative.IsNegative() {
		renderForm("commission must be zero or greater")
		return
	}

	tradeDate, err := time.Parse(time.DateOnly, r.FormValue("trade_date"))
	if err != nil {
		renderForm("invalid trade date")
		return
	}
	settledDate, err := time.Parse(time.DateOnly, r.FormValue("settled_date"))
	if err != nil {
		renderForm("invalid settled date")
		return
	}

	fxRateStr := r.FormValue("fx_rate")
	var fxRate *decimal.Decimal
	priceCAD := priceNative
	commCAD := commNative
	if fxRateStr != "" {
		fx, err := decimal.NewFromString(fxRateStr)
		if err != nil || !fx.IsPositive() {
			renderForm("FX rate must be greater than zero")
			return
		}
		fxRate = &fx
		priceCAD = priceNative.Mul(fx)
		commCAD = commNative.Mul(fx)
	} else if sec.Currency != "CAD" {
		renderForm("FX rate required for non-CAD securities")
		return
	}

	txType := transaction.Type(r.FormValue("type"))
	var validType bool
	for _, t := range txTypes {
		if t == txType {
			validType = true
			break
		}
	}
	if !validType {
		renderForm("invalid transaction type")
		return
	}

	tx := &transaction.Transaction{
		AccountID:        accountID,
		SecurityID:       securityID,
		Type:             txType,
		TradeDate:        tradeDate,
		SettledDate:      settledDate,
		Quantity:         qty,
		PriceNative:      priceNative,
		CommissionNative: commNative,
		FXRate:           fxRate,
		PriceCAD:         priceCAD,
		CommissionCAD:    commCAD,
		Source:           transaction.SourceManual,
		Notes:            r.FormValue("notes"),
	}

	if err := h.transactions.Create(r.Context(), tx); err != nil {
		renderForm("failed to save transaction")
		return
	}
	h.logAudit(r, audit.ActionCreate, audit.EntityTransaction, tx.ID, audit.Source(tx.Source), "")
	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
}

var fxCellTmpl = template.Must(template.New("fxcell").Parse(
	`{{if .FXRate}}{{.FXRate.StringFixed 4}} <button ` +
		`hx-get="/transactions/{{.ID}}/fx/edit" ` +
		`hx-target="#fx-cell-{{.ID}}" ` +
		`hx-swap="innerHTML" ` +
		`class="outline secondary" ` +
		`style="padding:0.1rem 0.4rem;font-size:0.75rem;margin:0">edit</button>{{else}}—{{end}}`,
))

var fxEditTmpl = template.Must(template.New("fxedit").Parse(
	`<form hx-post="/transactions/{{.ID}}/fx" ` +
		`hx-target="#fx-cell-{{.ID}}" ` +
		`hx-swap="innerHTML" ` +
		`style="display:flex;gap:0.25rem;align-items:center">` +
		`<input type="number" name="fx_rate" value="{{if .FXRate}}{{.FXRate.StringFixed 6}}{{end}}" ` +
		`step="0.000001" min="0.000001" required ` +
		`style="width:90px;padding:0.1rem 0.25rem;font-size:0.85rem;margin:0">` +
		`<button type="submit" class="outline" style="padding:0.1rem 0.4rem;font-size:0.75rem;margin:0">save</button>` +
		`<button type="button" ` +
		`hx-get="/transactions/{{.ID}}/fx/cell" ` +
		`hx-target="#fx-cell-{{.ID}}" ` +
		`hx-swap="innerHTML" ` +
		`class="outline secondary" style="padding:0.1rem 0.4rem;font-size:0.75rem;margin:0" aria-label="Cancel edit" title="Cancel">✕</button>` +
		`</form>`,
))

// fetchOwnedTx parses the {id} path value, loads the transaction, and verifies
// the transaction's account belongs to the current user. Writes the appropriate
// HTTP error and returns (nil, false) on any failure.
func (h *Handler) fetchOwnedTx(w http.ResponseWriter, r *http.Request) (*transaction.Transaction, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	tx, err := h.transactions.GetByID(r.Context(), id)
	if err != nil {
		h.notFoundOrError(w, r, err)
		return nil, false
	}
	acct, err := h.accounts.GetByID(r.Context(), tx.AccountID)
	if err != nil {
		loggerFromCtx(r.Context()).Error("fetchOwnedTx: account lookup", "account_id", tx.AccountID, "err", err)
		http.NotFound(w, r)
		return nil, false
	}
	if acct.UserID != userFromCtx(r.Context()).ID {
		http.NotFound(w, r)
		return nil, false
	}
	return tx, true
}

func (h *Handler) editTransactionFXForm(w http.ResponseWriter, r *http.Request) {
	tx, ok := h.fetchOwnedTx(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := fxEditTmpl.Execute(w, tx); err != nil {
		loggerFromCtx(r.Context()).Error("render fx edit form", "err", err)
	}
}

func (h *Handler) transactionFXCell(w http.ResponseWriter, r *http.Request) {
	tx, ok := h.fetchOwnedTx(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := fxCellTmpl.Execute(w, tx); err != nil {
		loggerFromCtx(r.Context()).Error("render fx cell", "err", err)
	}
}

func (h *Handler) updateTransactionFX(w http.ResponseWriter, r *http.Request) {
	tx, ok := h.fetchOwnedTx(w, r)
	if !ok {
		return
	}

	if tx.FXRate == nil {
		http.Error(w, "FX rate override not applicable to CAD transactions", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	fxRate, err := decimal.NewFromString(r.FormValue("fx_rate"))
	if err != nil || !fxRate.IsPositive() {
		http.Error(w, "FX rate must be greater than zero", http.StatusBadRequest)
		return
	}

	priceCAD := tx.PriceNative.Mul(fxRate)
	commCAD := tx.CommissionNative.Mul(fxRate)

	snapshot, err := json.Marshal(tx)
	if err != nil {
		loggerFromCtx(r.Context()).Error("snapshot marshal", "entity", "transaction", "id", tx.ID, "err", err)
	}
	if err := h.transactions.UpdateFXRate(r.Context(), tx.ID, &fxRate, priceCAD, commCAD); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.logAudit(r, audit.ActionUpdate, audit.EntityTransaction, tx.ID, audit.Source(tx.Source), string(snapshot))

	tx.FXRate = &fxRate
	tx.PriceCAD = priceCAD
	tx.CommissionCAD = commCAD

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := fxCellTmpl.Execute(w, tx); err != nil {
		loggerFromCtx(r.Context()).Error("render fx cell after update", "err", err)
	}
}

func (h *Handler) deleteTransaction(w http.ResponseWriter, r *http.Request) {
	tx, ok := h.fetchOwnedTx(w, r)
	if !ok {
		return
	}
	snapshot, err := json.Marshal(tx)
	if err != nil {
		loggerFromCtx(r.Context()).Error("snapshot marshal", "entity", "transaction", "id", tx.ID, "err", err)
	}
	if err := h.transactions.Delete(r.Context(), tx.ID); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.logAudit(r, audit.ActionDelete, audit.EntityTransaction, tx.ID, audit.Source(tx.Source), string(snapshot))
	w.WriteHeader(http.StatusOK)
}
