package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type qtPageData struct {
	Configured    bool
	Connected     bool
	Accounts      []qtAccountOption  // Pacioli accounts for "import into" select
	QTAccounts    []qtSourceAccount  // Questrade source accounts for "import from" select
	SyncResult    string
	NGResult      string
	Error         string
}

type qtAccountOption struct {
	ID   int64
	Name string
}

// qtSourceAccount is a Questrade account known to Pacioli (synced or manually created).
type qtSourceAccount struct {
	Number string
	Name   string
}

const (
	qtStatusOK   = "ok"
	qtStatusFlag = "flag"
	qtStatusSkip = "skip"
)

type qtPreviewRow struct {
	Line        int
	Status      string
	StatusMsg   string
	TxType      string
	TradeDate   string
	Symbol      string
	AccountName string
	Quantity    string
	Price       string
	Currency    string
	FXRate      string
	PriceCAD    string
	Commission  string
	CommCAD     string
}

type qtPreviewData struct {
	Rows       []qtPreviewRow
	CommitJSON string
	TotalOK    int
	TotalSkip  int
	TotalFlag  int
	Error      string
}

// qtSecRef is a lightweight security lookup result used during preview building.
type qtSecRef struct {
	ID       int64
	Currency string
}

// qtRowDisposition classifies how a processed activity should be counted and displayed.
type qtRowDisposition int

const (
	qtRowOK          qtRowDisposition = iota
	qtRowSilentSkip                   // QTActivitySkip: omit from preview, count as skip
	qtRowVisibleSkip                  // already-imported: show in preview, count as skip
	qtRowFlag                         // flagged: show in preview, count as flag
)

// qtPreviewCtx bundles per-request state threaded through processQTActivity.
// Maps are mutated in place to cache lookups across activities.
type qtPreviewCtx struct {
	pAccountID      int64
	pAccountName    string
	alreadyImported map[string]bool
	tickerCount     map[string]int
	secByTicker     map[string]qtSecRef
	notFoundTickers map[string]bool
	errTickers      map[string]string            // ticker → error for transient auto-create failures
	bocRates        map[time.Time]decimal.Decimal // date → USD/CAD rate, avoids redundant DB reads
	tryAutoCreate   func(ticker, currency string) (qtSecRef, error)
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
	token, err := h.qtTokens.Get(ctx, userFromCtx(r.Context()).ID)
	if err != nil {
		return questrade.Token{}, fmt.Errorf("load qt token: %w", err)
	}
	if !token.IsExpired() {
		return token, nil
	}
	// Refresh tokens are single-use; serialize concurrent refresh attempts so
	// only one goroutine calls Exchange. Re-check expiry after acquiring the
	// lock in case another goroutine already refreshed.
	h.tokenMu.Lock()
	defer h.tokenMu.Unlock()
	token, err = h.qtTokens.Get(ctx, userFromCtx(r.Context()).ID)
	if err != nil {
		return questrade.Token{}, fmt.Errorf("load qt token: %w", err)
	}
	if !token.IsExpired() {
		return token, nil
	}
	token, err = questrade.Exchange(ctx, token.RefreshToken.Reveal())
	if err != nil {
		return questrade.Token{}, fmt.Errorf("questrade token exchange: %w", err)
	}
	if err := h.qtTokens.Save(ctx, userFromCtx(r.Context()).ID, token); err != nil {
		return questrade.Token{}, fmt.Errorf("save refreshed qt token: %w", err)
	}
	return token, nil
}

func (h *Handler) questradePage(w http.ResponseWriter, r *http.Request) {
	if h.qtTokens == nil {
		h.render(w, r,"questrade", qtPageData{Configured: false, Error: "Questrade integration not configured — set TOKEN_ENCRYPTION_KEY to enable."})
		return
	}
	ctx := r.Context()
	_, err := h.qtTokens.Get(ctx, userFromCtx(r.Context()).ID)
	if err != nil && !errors.Is(err, errs.ErrNotFound) {
		h.serverError(w, r, err)
		return
	}
	connected := err == nil

	accounts, err := h.accounts.ListByUser(ctx, userFromCtx(r.Context()).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	opts := make([]qtAccountOption, len(accounts))
	var qtAccounts []qtSourceAccount
	for i, a := range accounts {
		opts[i] = qtAccountOption{a.ID, a.Name}
		if a.Broker == "Questrade" && a.AccountNumber != "" {
			qtAccounts = append(qtAccounts, qtSourceAccount{Number: a.AccountNumber, Name: a.Name})
		}
	}

	var syncResult, ngResult, errMsg string
	q := r.URL.Query()
	if sa := q.Get("sync_accounts"); sa != "" {
		ss := q.Get("sync_securities")
		syncResult = fmt.Sprintf("Sync complete — %s new account(s), %s new security(s) created.", sa, ss)
	}
	if n := q.Get("ng"); n != "" {
		if n == "0" {
			ngResult = "No unlinked Norbert's Gambit pairs found."
		} else {
			ngResult = fmt.Sprintf("Linked %s Norbert's Gambit pair(s).", n)
		}
	}
	if e := q.Get("error"); e != "" {
		errMsg = e
	}

	h.render(w, r, "questrade", qtPageData{
		Configured: true, Connected: connected,
		Accounts: opts, QTAccounts: qtAccounts,
		SyncResult: syncResult,
		NGResult:   ngResult,
		Error:      errMsg,
	})
}

func (h *Handler) questradeConnect(w http.ResponseWriter, r *http.Request) {
	if h.qtTokens == nil {
		http.Redirect(w, r, "/questrade", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}
	refreshToken := r.FormValue("refresh_token")
	if refreshToken == "" {
		h.render(w, r,"questrade", qtPageData{Configured: true, Error: "refresh token required"})
		return
	}

	token, err := questrade.Exchange(ctx, refreshToken)
	if err != nil {
		loggerFromCtx(ctx).Error("questrade connect", "err", err)
		accounts, err2 := h.accounts.ListByUser(ctx, userFromCtx(r.Context()).ID)
		if err2 != nil {
			h.serverError(w, r, err2)
			return
		}
		opts := make([]qtAccountOption, len(accounts))
		for i, a := range accounts {
			opts[i] = qtAccountOption{a.ID, a.Name}
		}
		h.render(w, r,"questrade", qtPageData{Configured: true, Accounts: opts, Error: "connection failed — check the token and try again"})
		return
	}
	if err := h.qtTokens.Save(ctx, userFromCtx(r.Context()).ID, token); err != nil {
		h.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/questrade", http.StatusSeeOther)
}

func (h *Handler) questradeDisconnect(w http.ResponseWriter, r *http.Request) {
	if h.qtTokens == nil {
		http.Redirect(w, r, "/questrade", http.StatusSeeOther)
		return
	}
	if err := h.qtTokens.Delete(r.Context(), userFromCtx(r.Context()).ID); err != nil {
		h.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/questrade", http.StatusSeeOther)
}

func (h *Handler) questradePreview(w http.ResponseWriter, r *http.Request) {
	if h.qtTokens == nil {
		http.Redirect(w, r, "/questrade", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	accounts, err := h.accounts.ListByUser(ctx, userFromCtx(r.Context()).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	opts := make([]qtAccountOption, len(accounts))
	for i, a := range accounts {
		opts[i] = qtAccountOption{a.ID, a.Name}
	}
	renderErr := func(msg string) {
		h.render(w, r, "questrade", qtPageData{Configured: true, Connected: true, Accounts: opts, Error: msg})
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
		renderErr("end date must be on or after start date")
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
		loggerFromCtx(ctx).Error("questrade fetch activities", "err", err)
		if errors.Is(err, questrade.ErrUnauthorized) {
			renderErr("Questrade token expired — reconnect via the button above")
		} else {
			renderErr("failed to fetch activities — check account number and date range")
		}
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
	secByTicker := make(map[string]qtSecRef, len(securities))
	for _, s := range securities {
		if tickerCount[s.Ticker] == 1 {
			secByTicker[s.Ticker] = qtSecRef{s.ID, s.Currency}
		}
	}

	existing, err := h.transactions.ListByDateRange(ctx, pAccountID, start, end.AddDate(0, 0, 1))
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	alreadyImported := make(map[string]bool, len(existing))
	for _, tx := range existing {
		// Only deduplicate against Questrade-sourced rows. Manual entries sharing
		// the same (secID, date, type, qty) should not block a QT import.
		if tx.Source != transaction.SourceQuestrade {
			continue
		}
		alreadyImported[qtImportKey(tx.SecurityID, tx.TradeDate, tx.Type, tx.Quantity)] = true
		if tx.Type == transaction.TypeFXConversion {
			alreadyImported[qtImportKey(tx.SecurityID, tx.TradeDate, transaction.TypeTransferOut, tx.Quantity)] = true
		}
	}

	// tryAutoCreateSecurity calls Questrade symbol search and creates the security in the DB
	// if an exact ticker match is found. Results are idempotent — a second call for the same
	// ticker finds the security via secByTicker.
	tryAutoCreateSecurity := func(ticker, currency string) (qtSecRef, error) {
		results, err := client.SymbolSearch(ctx, ticker)
		if err != nil {
			return qtSecRef{}, fmt.Errorf("symbol search: %w", err)
		}
		sec := &security.Security{
			Ticker:   ticker,
			Type:     security.TypeEquity,
			Currency: currency,
			Source:   string(audit.SourceQuestrade),
		}
		for _, sr := range results {
			if sr.Symbol != ticker {
				continue
			}
			sec.Exchange = sr.Exchange
			sec.Name = sr.Description
			sec.Type = mapQTSecurityType(sr.SecurityType)
			if validCurrencies[sr.Currency] {
				sec.Currency = sr.Currency
			}
			break
		}
		if sec.Exchange == "" {
			return qtSecRef{}, errs.ErrNotFound
		}
		if err := h.securities.Create(ctx, sec); err != nil {
			return qtSecRef{}, fmt.Errorf("create security: %w", err)
		}
		h.logAudit(r, audit.ActionCreate, audit.EntitySecurity, sec.ID, audit.SourceQuestrade, "")
		return qtSecRef{sec.ID, sec.Currency}, nil
	}

	notFoundTickers := make(map[string]bool)
	var previewRows []qtPreviewRow
	var commitRows []qtCommitRow
	totOK, totSkip, totFlag := 0, 0, 0

	pctx := &qtPreviewCtx{
		pAccountID:      pAccountID,
		pAccountName:    pAccountName,
		alreadyImported: alreadyImported,
		tickerCount:     tickerCount,
		secByTicker:     secByTicker,
		notFoundTickers: notFoundTickers,
		errTickers:      make(map[string]string),
		bocRates:        make(map[time.Time]decimal.Decimal),
		tryAutoCreate:   tryAutoCreateSecurity,
	}

	for i := range activities {
		previewRow, commitRow, disp := h.processQTActivity(ctx, i+1, &activities[i], pctx)
		switch disp {
		case qtRowOK:
			totOK++
			previewRows = append(previewRows, previewRow)
			commitRows = append(commitRows, *commitRow)
		case qtRowSilentSkip:
			totSkip++
		case qtRowVisibleSkip:
			totSkip++
			previewRows = append(previewRows, previewRow)
		case qtRowFlag:
			totFlag++
			previewRows = append(previewRows, previewRow)
		}
	}

	commitJSON, err := json.Marshal(commitRows)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r,"questrade_preview", qtPreviewData{
		Rows:       previewRows,
		CommitJSON: string(commitJSON),
		TotalOK:    totOK,
		TotalSkip:  totSkip,
		TotalFlag:  totFlag,
	})
}

// processQTActivity maps a single Questrade activity to a preview row, an optional
// commit row, and a disposition that tells the caller how to count and display the result.
// act.Price may be mutated for CAD-reported USD TFI activities (ClassifyQTActivity sets it from description).
// pctx caches are updated in place (secByTicker, notFoundTickers).
func (h *Handler) processQTActivity(
	ctx context.Context,
	lineNum int,
	act *questrade.Activity,
	pctx *qtPreviewCtx,
) (qtPreviewRow, *qtCommitRow, qtRowDisposition) {
	status, msg, txType := service.ClassifyQTActivity(act)

	if status == service.QTActivitySkip {
		return qtPreviewRow{}, nil, qtRowSilentSkip
	}

	baseRow := qtPreviewRow{
		Line:        lineNum,
		TradeDate:   act.TradeDate.Format(time.DateOnly),
		Symbol:      act.Symbol,
		Currency:    act.Currency,
		TxType:      string(txType),
		AccountName: pctx.pAccountName,
	}

	flagRow := func(reason string) (qtPreviewRow, *qtCommitRow, qtRowDisposition) {
		baseRow.Status = qtStatusFlag
		baseRow.StatusMsg = reason
		return baseRow, nil, qtRowFlag
	}

	if status == service.QTActivityFlag {
		if strings.TrimSpace(act.Action) == "FXT" && !act.NetAmount.IsZero() {
			msg += fmt.Sprintf(" — net: %s %s", act.NetAmount.StringFixed(2), act.Currency)
			if act.Type != "" {
				msg += " (" + act.Type + ")"
			}
		}
		return flagRow(msg)
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
		return flagRow("zero quantity — record manually")
	}

	sec, secOK := pctx.secByTicker[act.Symbol]
	_, hadErrBefore := pctx.errTickers[act.Symbol]
	if !secOK && pctx.tickerCount[act.Symbol] <= 1 && !pctx.notFoundTickers[act.Symbol] && !hadErrBefore {
		if ref, err := pctx.tryAutoCreate(act.Symbol, act.Currency); err == nil {
			pctx.secByTicker[act.Symbol] = ref
			sec = ref
			secOK = true
		} else if errors.Is(err, errs.ErrNotFound) {
			pctx.notFoundTickers[act.Symbol] = true
		} else {
			pctx.errTickers[act.Symbol] = err.Error()
		}
	}
	if !secOK {
		if pctx.tickerCount[act.Symbol] > 1 {
			return flagRow("ambiguous ticker — multiple securities share: " + act.Symbol)
		}
		if errMsg, ok := pctx.errTickers[act.Symbol]; ok {
			return flagRow("security lookup failed for " + act.Symbol + ": " + errMsg)
		}
		return flagRow("security not found — add security with ticker: " + act.Symbol)
	}

	bocRate := func(date time.Time) (decimal.Decimal, error) {
		key := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		if r, ok := pctx.bocRates[key]; ok {
			return r, nil
		}
		r, err := h.bocSvc.USDCADRate(ctx, date)
		if err != nil {
			return decimal.Zero, err
		}
		pctx.bocRates[key] = r
		return r, nil
	}

	// Activity currency must match the security's currency in the DB.
	// Exception: Questrade reports TFI activities for USD securities with currency="CAD"
	// because the book value in the description is the CAD equivalent of the USD cost.
	// Convert the extracted CAD price to USD using the BoC rate so ACB is stored correctly.
	// fxRateStr is set here for converted TFIs to avoid a redundant fetch below.
	var fxRate decimal.Decimal
	var fxRateStr string
	if act.Currency != sec.Currency {
		if txType == transaction.TypeTransferIn && act.Currency == "CAD" && sec.Currency == "USD" && price.IsPositive() {
			var err error
			fxRate, err = bocRate(act.TradeDate)
			if err != nil {
				return flagRow("BoC USD/CAD rate unavailable for " + act.TradeDate.Format(time.DateOnly) + ": " + err.Error())
			}
			price = price.Div(fxRate)
			comm = comm.Div(fxRate)
			fxRateStr = fxRate.String()
			baseRow.Currency = "USD"
		} else {
			return flagRow("currency mismatch: activity is " + act.Currency + " but security is " + sec.Currency)
		}
	}

	// Only CAD and USD are supported; flag anything else before it reaches commit.
	if act.Currency != "CAD" && act.Currency != "USD" {
		return flagRow("unsupported currency (" + act.Currency + ") — only CAD and USD securities can be imported")
	}

	if pctx.alreadyImported[qtImportKey(sec.ID, act.TradeDate, txType, qty)] {
		baseRow.Status = qtStatusSkip
		baseRow.StatusMsg = "already imported — skipped"
		return baseRow, nil, qtRowVisibleSkip
	}

	// Fetch BoC rate for display and to verify it's available before committing.
	// Skip if already fetched above (CAD-reported USD TFI conversion).
	if act.Currency == "USD" && fxRateStr == "" {
		var err error
		fxRate, err = bocRate(act.TradeDate)
		if err != nil {
			return flagRow("BoC USD/CAD rate unavailable for " + act.TradeDate.Format(time.DateOnly) + ": " + err.Error())
		}
		fxRateStr = fxRate.String()
	}

	baseRow.Status = qtStatusOK
	baseRow.Quantity = qty.String()
	baseRow.Price = price.StringFixed(4)
	baseRow.FXRate = fxRateStr
	baseRow.Commission = comm.StringFixed(2)
	if fxRateStr != "" {
		baseRow.PriceCAD = price.Mul(fxRate).StringFixed(4)
		baseRow.CommCAD = comm.Mul(fxRate).StringFixed(2)
	}

	// CAD amounts are NOT stored in the commit payload — derived server-side
	// at commit time from the security's currency + BoC cache.
	commitRow := qtCommitRow{
		TradeDate:   act.TradeDate.Format(time.DateOnly),
		SettledDate: act.SettledDate.Format(time.DateOnly),
		AccountID:   pctx.pAccountID,
		SecurityID:  sec.ID,
		TxType:      string(txType),
		Quantity:    qty.String(),
		PriceNative: price.String(),
		CommNative:  comm.String(),
	}
	return baseRow, &commitRow, qtRowOK
}

// qtImportKey returns the dedup key used to identify an already-imported QT
// activity. TypeFXConversion gets an alias key of TypeTransferOut because a
// linked NG give-leg is re-typed in the DB but QT still reports it as a
// negative FXT (transfer_out).
func qtImportKey(secID int64, date time.Time, txType transaction.Type, qty decimal.Decimal) string {
	return fmt.Sprintf("%d|%s|%s|%s", secID, date.Format(time.DateOnly), string(txType), qty.String())
}

// validQTTypes restricts commit to types the Questrade preview can actually produce.
var validQTTypes = map[transaction.Type]bool{
	transaction.TypeBuy:         true,
	transaction.TypeSell:        true,
	transaction.TypeDividend:    true,
	transaction.TypeJournal:     true,
	transaction.TypeTransferOut: true,
	transaction.TypeTransferIn:  true,
}

func (h *Handler) questradeCommit(w http.ResponseWriter, r *http.Request) {
	if h.qtTokens == nil {
		http.Redirect(w, r, "/questrade", http.StatusSeeOther)
		return
	}
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
	sess, err := h.newImportSession(ctx, userFromCtx(r.Context()).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	for i := range commitRows {
		cr := &commitRows[i]

		if !sess.ownedAccounts[cr.AccountID] {
			loggerFromCtx(ctx).Warn("qt commit: account not owned", "account_id", cr.AccountID)
			continue
		}

		txType := transaction.Type(cr.TxType)
		if !validQTTypes[txType] {
			loggerFromCtx(ctx).Warn("qt commit: invalid tx type", "type", cr.TxType)
			continue
		}

		sec, err := sess.lookupSec(cr.SecurityID)
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
			loggerFromCtx(ctx).Error("qt commit: invalid quantity", "val", cr.Quantity, "err", err)
			continue
		}
		priceNative, err := decimal.NewFromString(cr.PriceNative)
		if err != nil || priceNative.IsNegative() {
			loggerFromCtx(ctx).Error("qt commit: invalid price native", "val", cr.PriceNative, "err", err)
			continue
		}
		commNative, err := decimal.NewFromString(cr.CommNative)
		if err != nil || commNative.IsNegative() {
			loggerFromCtx(ctx).Error("qt commit: invalid comm native", "val", cr.CommNative, "err", err)
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
			UserID:     userFromCtx(r.Context()).ID,
			UserEmail:  userFromCtx(r.Context()).Email,
			Action:     audit.ActionCreate,
			EntityType: audit.EntityTransaction,
			EntityID:   tx.ID,
			Source:     audit.SourceQuestrade,
			ImportID:   sess.importID,
		}); err != nil {
			loggerFromCtx(ctx).Error("qt commit: audit log", "err", err)
		}
	}

	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
}

// questradeSync fetches all Questrade accounts and their positions, creating any
// missing Pacioli accounts and securities.
func (h *Handler) questradeSync(w http.ResponseWriter, r *http.Request) {
	if h.qtTokens == nil {
		http.Redirect(w, r, "/questrade", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	log := loggerFromCtx(ctx)

	token, err := h.activeToken(r)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			http.Redirect(w, r, "/questrade", http.StatusSeeOther)
			return
		}
		h.serverError(w, r, err)
		return
	}

	client := questrade.New(token)

	qtAccounts, err := client.Accounts(ctx)
	if err != nil {
		log.Error("questrade sync: fetch accounts", "err", err)
		h.serverError(w, r, err)
		return
	}

	existing, err := h.accounts.ListByUser(ctx, userFromCtx(r.Context()).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	byNumber := make(map[string]*account.Account, len(existing))
	for _, a := range existing {
		if a.AccountNumber != "" {
			byNumber[a.AccountNumber] = a
		}
	}

	var newAccounts, newSecurities int

	for _, qa := range qtAccounts {
		if _, ok := byNumber[qa.Number]; ok {
			continue
		}
		acType := mapQTAccountType(qa.Type)
		a := &account.Account{
			UserID:        userFromCtx(r.Context()).ID,
			Name:          qa.Type + " " + qa.Number,
			Type:          acType,
			Broker:        "Questrade",
			Currency:      "CAD",
			AccountNumber: qa.Number,
			Source:        string(audit.SourceQuestrade),
		}
		if err := h.accounts.Create(ctx, a); err != nil {
			log.Error("questrade sync: create account", "number", qa.Number, "err", err)
			continue
		}
		h.logAudit(r, audit.ActionCreate, audit.EntityAccount, a.ID, audit.SourceQuestrade, "")
		byNumber[qa.Number] = a
		newAccounts++
	}

	existingSecs, err := h.securities.ListAll(ctx)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	secByTickerExchange := make(map[string]*security.Security, len(existingSecs))
	for _, s := range existingSecs {
		secByTickerExchange[s.Ticker+"|"+s.Exchange] = s
	}
	symbolCache := make(map[string][]questrade.SymbolInfo)

	for _, qa := range qtAccounts {
		positions, err := client.Positions(ctx, qa.Number)
		if err != nil {
			log.Error("questrade sync: fetch positions", "account", qa.Number, "err", err)
			continue
		}
		for _, p := range positions {
			ticker := p.Symbol
			currency := p.Currency
			if !validCurrencies[currency] {
				currency = "CAD"
			}
			sec := &security.Security{
				Ticker:   ticker,
				Name:     ticker,
				Type:     security.TypeEquity,
				Currency: currency,
				Source:   string(audit.SourceQuestrade),
			}
			// Symbol search fills exchange/name/type/currency — must run before
			// the existence check so we key on the correct (ticker, exchange) pair.
			// Results are cached per ticker to avoid repeated API calls across accounts.
			if _, cached := symbolCache[ticker]; !cached {
				results, err := client.SymbolSearch(ctx, ticker)
				if err == nil {
					symbolCache[ticker] = results
				} else {
					symbolCache[ticker] = nil
				}
			}
			if results := symbolCache[ticker]; len(results) > 0 {
				sr := results[0]
				sec.Exchange = sr.Exchange
				sec.Name = sr.Description
				sec.Type = mapQTSecurityType(sr.SecurityType)
				if validCurrencies[sr.Currency] {
					sec.Currency = sr.Currency
				}
			}
			if _, ok := secByTickerExchange[ticker+"|"+sec.Exchange]; ok {
				continue
			}
			if err := h.securities.Create(ctx, sec); err != nil {
				log.Error("questrade sync: create security", "ticker", ticker, "err", err)
				continue
			}
			h.logAudit(r, audit.ActionCreate, audit.EntitySecurity, sec.ID, audit.SourceQuestrade, "")
			secByTickerExchange[ticker+"|"+sec.Exchange] = sec
			newSecurities++
		}
	}

	http.Redirect(w, r,
		fmt.Sprintf("/questrade?sync_accounts=%d&sync_securities=%d", newAccounts, newSecurities),
		http.StatusSeeOther)
}

func mapQTAccountType(qt string) account.Type {
	switch strings.ToUpper(qt) {
	case "TFSA":
		return account.TypeTFSA
	case "RRSP", "SRRSP": // spousal RRSP
		return account.TypeRRSP
	case "LRRSP", "LRSP": // locked-in RRSP / LRSP
		return account.TypeLRSP
	case "RESP", "FRESP": // individual / family RESP
		return account.TypeRESP
	case "SRSP":
		return account.TypeSRSP
	case "MARGIN":
		return account.TypeMargin
	default:
		return account.TypeCash
	}
}

