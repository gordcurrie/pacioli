package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type qtPageData struct {
	Connected bool
	Accounts  []qtAccountOption
	Error     string
}

type qtAccountOption struct {
	ID   int64
	Name string
}

type qtPreviewRow struct {
	Line        int
	Status      string // "ok" | "flag"
	StatusMsg   string
	TxType      string
	TradeDate   string
	Symbol      string
	AccountName string
	Quantity    string
	Price       string
	Currency    string
	FXRate      string
	Commission  string
}

type qtPreviewData struct {
	Rows       []qtPreviewRow
	CommitJSON string
	TotalOK    int
	TotalSkip  int
	TotalFlag  int
	Error      string
}

// qtCommitRow is the JSON payload passed from preview to commit.
// CAD amounts and FX rates are NOT included — the commit handler derives them
// server-side from the security's currency and the BoC cache.
type qtCommitRow struct {
	TradeDate   string `json:"td"`
	SettledDate string `json:"sd"`
	AccountID   int64  `json:"aid"`
	SecurityID  int64  `json:"sid"`
	TxType      string `json:"t"`
	Quantity    string `json:"q"`
	PriceNative string `json:"pn"`
	CommNative  string `json:"cn"`
	Notes       string `json:"n"`
}

// activeToken loads the stored token, refreshing and re-saving if near expiry.
func (h *Handler) activeToken(r *http.Request) (questrade.Token, error) {
	ctx := r.Context()
	token, err := h.qtTokens.Get(ctx, h.userID)
	if err != nil {
		return questrade.Token{}, err
	}
	if !token.IsExpired() {
		return token, nil
	}
	token, err = questrade.Exchange(ctx, token.RefreshToken.Reveal())
	if err != nil {
		return questrade.Token{}, err
	}
	if err := h.qtTokens.Save(ctx, h.userID, token); err != nil {
		loggerFromCtx(ctx).Error("save refreshed qt token", "err", err)
	}
	return token, nil
}

func (h *Handler) questradePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, err := h.qtTokens.Get(ctx, h.userID)
	connected := err == nil

	accounts, _ := h.accounts.ListByUser(ctx, h.userID)
	opts := make([]qtAccountOption, len(accounts))
	for i, a := range accounts {
		opts[i] = qtAccountOption{a.ID, a.Name}
	}
	h.render(w, "questrade", qtPageData{Connected: connected, Accounts: opts})
}

func (h *Handler) questradeConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	refreshToken := r.FormValue("refresh_token")
	if refreshToken == "" {
		h.render(w, "questrade", qtPageData{Error: "refresh token required"})
		return
	}

	token, err := questrade.Exchange(ctx, refreshToken)
	if err != nil {
		loggerFromCtx(ctx).Error("questrade connect", "err", err)
		accounts, _ := h.accounts.ListByUser(ctx, h.userID)
		opts := make([]qtAccountOption, len(accounts))
		for i, a := range accounts {
			opts[i] = qtAccountOption{a.ID, a.Name}
		}
		h.render(w, "questrade", qtPageData{Accounts: opts, Error: "connection failed: " + err.Error()})
		return
	}
	if err := h.qtTokens.Save(ctx, h.userID, token); err != nil {
		h.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/questrade", http.StatusSeeOther)
}

func (h *Handler) questradeDisconnect(w http.ResponseWriter, r *http.Request) {
	_ = h.qtTokens.Delete(r.Context(), h.userID)
	http.Redirect(w, r, "/questrade", http.StatusSeeOther)
}

func (h *Handler) questradePreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	renderErr := func(msg string) {
		accounts, _ := h.accounts.ListByUser(ctx, h.userID)
		opts := make([]qtAccountOption, len(accounts))
		for i, a := range accounts {
			opts[i] = qtAccountOption{a.ID, a.Name}
		}
		h.render(w, "questrade", qtPageData{Connected: true, Accounts: opts, Error: msg})
	}

	token, err := h.activeToken(r)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			http.Redirect(w, r, "/questrade", http.StatusSeeOther)
			return
		}
		renderErr("token error: " + err.Error())
		return
	}

	qtAccountNo := r.FormValue("qt_account")
	if qtAccountNo == "" {
		renderErr("Questrade account number required")
		return
	}

	pAccountID, err := strconv.ParseInt(r.FormValue("pacioli_account"), 10, 64)
	if err != nil || pAccountID <= 0 {
		renderErr("select a valid account")
		return
	}

	startStr := r.FormValue("start_date")
	endStr := r.FormValue("end_date")
	start, err := time.Parse(time.DateOnly, startStr)
	if err != nil {
		renderErr("invalid start date")
		return
	}
	end, err := time.Parse(time.DateOnly, endStr)
	if err != nil {
		renderErr("invalid end date")
		return
	}
	if end.Before(start) {
		renderErr("end date must be after start date")
		return
	}

	// validate account ownership
	accounts, err := h.accounts.ListByUser(ctx, h.userID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	var pAccountName string
	owned := false
	for _, a := range accounts {
		if a.ID == pAccountID {
			pAccountName = a.Name
			owned = true
			break
		}
	}
	if !owned {
		renderErr("account not found or not owned")
		return
	}

	client := questrade.New(token)
	// end + 1 day gives an exclusive upper bound covering the entire end date in any timezone.
	activities, err := client.Activities(ctx, qtAccountNo, start, end.AddDate(0, 0, 1))
	if err != nil {
		renderErr("failed to fetch activities: " + err.Error())
		return
	}

	securities, err := h.securities.ListAll(ctx)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	tickerCount := make(map[string]int, len(securities))
	for _, s := range securities {
		tickerCount[s.Ticker]++
	}
	type secRef struct {
		ID       int64
		Currency string
	}
	secByTicker := make(map[string]secRef, len(securities))
	for _, s := range securities {
		if tickerCount[s.Ticker] == 1 {
			secByTicker[s.Ticker] = secRef{s.ID, s.Currency}
		}
	}

	var previewRows []qtPreviewRow
	var commitRows []qtCommitRow
	totOK, totSkip, totFlag := 0, 0, 0

	for i := range activities {
		act := &activities[i]
		status, msg, txType := classifyQTActivity(act)

		if status == qtSkip {
			totSkip++
			continue
		}

		baseRow := qtPreviewRow{
			Line:      i + 1,
			TradeDate: act.TradeDate.Format(time.DateOnly),
			Symbol:    act.Symbol,
			Currency:  act.Currency,
		}

		if status == qtFlag {
			totFlag++
			baseRow.Status = "flag"
			baseRow.StatusMsg = msg
			previewRows = append(previewRows, baseRow)
			continue
		}

		qty := act.Quantity.Abs()
		price := act.Price
		comm := act.Commission.Abs()

		// Dividends from Questrade have zero qty/price; store as 1 unit at total amount.
		if txType == transaction.TypeDividend && qty.IsZero() && price.IsZero() {
			qty = decimal.New(1, 0)
			price = act.NetAmount.Abs()
		}

		if !qty.IsPositive() {
			totFlag++
			baseRow.Status = "flag"
			baseRow.StatusMsg = "zero quantity — record manually"
			previewRows = append(previewRows, baseRow)
			continue
		}

		sec, secOK := secByTicker[act.Symbol]
		if !secOK {
			totFlag++
			baseRow.Status = "flag"
			if tickerCount[act.Symbol] > 1 {
				baseRow.StatusMsg = "ambiguous ticker — multiple securities share: " + act.Symbol
			} else {
				baseRow.StatusMsg = "security not found — add security with ticker: " + act.Symbol
			}
			previewRows = append(previewRows, baseRow)
			continue
		}

		// Activity currency must match the security's currency in the DB.
		if act.Currency != sec.Currency {
			totFlag++
			baseRow.Status = "flag"
			baseRow.StatusMsg = "currency mismatch: activity is " + act.Currency + " but security is " + sec.Currency
			previewRows = append(previewRows, baseRow)
			continue
		}

		// Fetch BoC rate for display and to verify it's available before committing.
		var fxRateStr string
		if act.Currency == "USD" {
			fxRate, err := h.bocSvc.USDCADRate(ctx, act.TradeDate)
			if err != nil {
				totFlag++
				baseRow.Status = "flag"
				baseRow.StatusMsg = "BoC USD/CAD rate unavailable for " + act.TradeDate.Format(time.DateOnly) + ": " + err.Error()
				previewRows = append(previewRows, baseRow)
				continue
			}
			fxRateStr = fxRate.String()
		}

		totOK++
		baseRow.Status = "ok"
		baseRow.TxType = string(txType)
		baseRow.AccountName = pAccountName
		baseRow.Quantity = qty.String()
		baseRow.Price = price.StringFixed(4)
		baseRow.FXRate = fxRateStr
		baseRow.Commission = comm.StringFixed(2)
		previewRows = append(previewRows, baseRow)

		// CAD amounts are NOT stored in the commit payload — derived server-side
		// at commit time from the security's currency + BoC cache.
		commitRows = append(commitRows, qtCommitRow{
			TradeDate:   act.TradeDate.Format(time.DateOnly),
			SettledDate: act.SettledDate.Format(time.DateOnly),
			AccountID:   pAccountID,
			SecurityID:  sec.ID,
			TxType:      string(txType),
			Quantity:    qty.String(),
			PriceNative: price.String(),
			CommNative:  comm.String(),
		})
	}

	commitJSON, err := json.Marshal(commitRows)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, "questrade_preview", qtPreviewData{
		Rows:       previewRows,
		CommitJSON: string(commitJSON),
		TotalOK:    totOK,
		TotalSkip:  totSkip,
		TotalFlag:  totFlag,
	})
}

type qtStatus int

const (
	qtImport qtStatus = iota
	qtSkip
	qtFlag
)

func classifyQTActivity(a *questrade.Activity) (qtStatus, string, transaction.Type) {
	switch a.Action {
	case "Buy":
		return qtImport, "", transaction.TypeBuy
	case "Sell":
		return qtImport, "", transaction.TypeSell
	case "DIV", "INT":
		return qtImport, "", transaction.TypeDividend
	case "REI":
		// Dividend reinvestment: treated as a buy (acquires shares, increases ACB)
		return qtImport, "", transaction.TypeBuy
	case "CON", "WDR", "DEP", "TFI", "TFO", "EXP", "":
		return qtSkip, "", ""
	case "FXT":
		return qtFlag, "FX conversion — may be Norbert's Gambit; enter manually", ""
	default:
		return qtFlag, "unknown action: " + a.Action, ""
	}
}

func (h *Handler) questradeCommit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 30<<20)
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	var commitRows []qtCommitRow
	if err := json.Unmarshal([]byte(r.FormValue("commit_rows")), &commitRows); err != nil || len(commitRows) == 0 {
		http.Redirect(w, r, "/questrade", http.StatusSeeOther)
		return
	}

	ctx := r.Context()
	accounts, err := h.accounts.ListByUser(ctx, h.userID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	ownedAccounts := make(map[int64]bool, len(accounts))
	for _, a := range accounts {
		ownedAccounts[a.ID] = true
	}

	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		h.serverError(w, r, err)
		return
	}
	importID := hex.EncodeToString(b[:])

	type cachedSec struct {
		currency string
		found    bool
	}
	secCache := make(map[int64]cachedSec)
	lookupSec := func(id int64) (cachedSec, error) {
		if s, ok := secCache[id]; ok {
			return s, nil
		}
		sec, err := h.securities.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				secCache[id] = cachedSec{}
				return cachedSec{}, nil
			}
			return cachedSec{}, err
		}
		cs := cachedSec{currency: sec.Currency, found: true}
		secCache[id] = cs
		return cs, nil
	}

	for i := range commitRows {
		cr := &commitRows[i]

		if !ownedAccounts[cr.AccountID] {
			loggerFromCtx(ctx).Warn("qt commit: account not owned", "account_id", cr.AccountID)
			continue
		}

		txType := transaction.Type(cr.TxType)
		if !validImportTypes[txType] {
			loggerFromCtx(ctx).Warn("qt commit: invalid tx type", "type", cr.TxType)
			continue
		}

		sec, err := lookupSec(cr.SecurityID)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		if !sec.found {
			loggerFromCtx(ctx).Warn("qt commit: security not found", "security_id", cr.SecurityID)
			continue
		}

		tradeDate, err := time.Parse(time.DateOnly, cr.TradeDate)
		if err != nil {
			loggerFromCtx(ctx).Error("qt commit: parse trade date", "err", err)
			continue
		}
		settledDate, err := time.Parse(time.DateOnly, cr.SettledDate)
		if err != nil {
			settledDate = tradeDate
		}

		qty, err := decimal.NewFromString(cr.Quantity)
		if err != nil || !qty.IsPositive() {
			loggerFromCtx(ctx).Error("qt commit: invalid quantity", "val", cr.Quantity)
			continue
		}
		priceNative, err := decimal.NewFromString(cr.PriceNative)
		if err != nil || priceNative.IsNegative() {
			loggerFromCtx(ctx).Error("qt commit: invalid price native", "val", cr.PriceNative)
			continue
		}
		commNative, err := decimal.NewFromString(cr.CommNative)
		if err != nil || commNative.IsNegative() {
			loggerFromCtx(ctx).Error("qt commit: invalid comm native", "val", cr.CommNative)
			continue
		}

		// Derive CAD amounts server-side from the security's authoritative currency.
		priceCAD := priceNative
		commCAD := commNative
		var fxRate *decimal.Decimal

		switch sec.currency {
		case "CAD":
			// no conversion needed
		case "USD":
			rate, err := h.bocSvc.USDCADRate(ctx, tradeDate)
			if err != nil {
				loggerFromCtx(ctx).Error("qt commit: BoC rate unavailable", "date", cr.TradeDate, "err", err)
				continue
			}
			priceCAD = priceNative.Mul(rate)
			commCAD = commNative.Mul(rate)
			fxRate = &rate
		default:
			loggerFromCtx(ctx).Warn("qt commit: unsupported security currency", "currency", sec.currency, "security_id", cr.SecurityID)
			continue
		}

		tx := &transaction.Transaction{
			AccountID:        cr.AccountID,
			SecurityID:       cr.SecurityID,
			Type:             txType,
			TradeDate:        tradeDate,
			SettledDate:      settledDate,
			Quantity:         qty,
			PriceNative:      priceNative,
			CommissionNative: commNative,
			FXRate:           fxRate,
			PriceCAD:         priceCAD,
			CommissionCAD:    commCAD,
			Source:           transaction.SourceQuestrade,
			Notes:            cr.Notes,
		}

		if err := h.transactions.Create(ctx, tx); err != nil {
			loggerFromCtx(ctx).Error("qt commit: create transaction", "err", err)
			continue
		}

		if err := h.audits.Log(ctx, &audit.Entry{
			UserID:     h.userID,
			Action:     audit.ActionCreate,
			EntityType: audit.EntityTransaction,
			EntityID:   tx.ID,
			Source:     audit.SourceQuestrade,
			ImportID:   importID,
		}); err != nil {
			loggerFromCtx(ctx).Error("qt commit: audit log", "err", err)
		}
	}

	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
}
