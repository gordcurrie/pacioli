package handler

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gordcurrie/pacioli/internal/account"
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
	accounts, err := h.accounts.ListByUser(r.Context(), h.userID)
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

	h.render(w, "transactions", transactionsPageData{Transactions: rows})
}

func (h *Handler) newTransaction(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.accounts.ListByUser(r.Context(), h.userID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, "transaction_form", transactionFormData{
		Accounts: accounts,
		Types:    txTypes,
	})
}

func (h *Handler) createTransaction(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.accounts.ListByUser(r.Context(), h.userID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	renderForm := func(errMsg string) {
		h.render(w, "transaction_form", transactionFormData{
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
	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
}

func (h *Handler) deleteTransaction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tx, err := h.transactions.GetByID(r.Context(), id)
	if err != nil {
		h.notFoundOrError(w, r, err)
		return
	}
	acct, err := h.accounts.GetByID(r.Context(), tx.AccountID)
	if err != nil || acct.UserID != h.userID {
		http.NotFound(w, r)
		return
	}
	if err := h.transactions.Delete(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
