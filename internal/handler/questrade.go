package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type qtPageData struct {
	Configured    bool
	Connected     bool
	Accounts      []qtAccountOption  // Pacioli accounts for "import into" select
	QTAccounts    []qtSourceAccount  // Questrade source accounts for "import from" select
	SyncResult    string
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
	// Refresh tokens are single-use; serialize concurrent refresh attempts so
	// only one goroutine calls Exchange. Re-check expiry after acquiring the
	// lock in case another goroutine already refreshed.
	h.tokenMu.Lock()
	defer h.tokenMu.Unlock()
	token, err = h.qtTokens.Get(ctx, h.userID)
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
		return questrade.Token{}, fmt.Errorf("save refreshed qt token: %w", err)
	}
	return token, nil
}

func (h *Handler) questradePage(w http.ResponseWriter, r *http.Request) {
	if h.qtTokens == nil {
		h.render(w, "questrade", qtPageData{Configured: false, Error: "Questrade integration not configured — set TOKEN_ENCRYPTION_KEY to enable."})
		return
	}
	ctx := r.Context()
	_, err := h.qtTokens.Get(ctx, h.userID)
	if err != nil && !errors.Is(err, errs.ErrNotFound) {
		h.serverError(w, r, err)
		return
	}
	connected := err == nil

	accounts, err := h.accounts.ListByUser(ctx, h.userID)
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

	var syncResult string
	if sa := r.URL.Query().Get("sync_accounts"); sa != "" {
		ss := r.URL.Query().Get("sync_securities")
		syncResult = fmt.Sprintf("Sync complete — %s new account(s), %s new security(s) created.", sa, ss)
	}

	h.render(w, "questrade", qtPageData{
		Configured: true, Connected: connected,
		Accounts: opts, QTAccounts: qtAccounts,
		SyncResult: syncResult,
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
		h.render(w, "questrade", qtPageData{Configured: true, Error: "refresh token required"})
		return
	}

	token, err := questrade.Exchange(ctx, refreshToken)
	if err != nil {
		loggerFromCtx(ctx).Error("questrade connect", "err", err)
		accounts, err2 := h.accounts.ListByUser(ctx, h.userID)
		if err2 != nil {
			h.serverError(w, r, err2)
			return
		}
		opts := make([]qtAccountOption, len(accounts))
		for i, a := range accounts {
			opts[i] = qtAccountOption{a.ID, a.Name}
		}
		h.render(w, "questrade", qtPageData{Configured: true, Accounts: opts, Error: "connection failed — check the token and try again"})
		return
	}
	if err := h.qtTokens.Save(ctx, h.userID, token); err != nil {
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
	if err := h.qtTokens.Delete(r.Context(), h.userID); err != nil {
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

	renderErr := func(msg string) {
		accounts, err := h.accounts.ListByUser(ctx, h.userID)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		opts := make([]qtAccountOption, len(accounts))
		for i, a := range accounts {
			opts[i] = qtAccountOption{a.ID, a.Name}
		}
		h.render(w, "questrade", qtPageData{Configured: true, Connected: true, Accounts: opts, Error: msg})
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
		loggerFromCtx(ctx).Error("questrade fetch activities", "err", err)
		renderErr("failed to fetch activities — check account number and date range")
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
			Line:        i + 1,
			TradeDate:   act.TradeDate.Format(time.DateOnly),
			Symbol:      act.Symbol,
			Currency:    act.Currency,
			TxType:      string(txType),
			AccountName: pAccountName,
		}

		if status == qtFlag {
			totFlag++
			baseRow.Status = qtStatusFlag
			baseRow.StatusMsg = msg
			if strings.TrimSpace(act.Action) == "FXT" && !act.NetAmount.IsZero() {
				baseRow.StatusMsg += fmt.Sprintf(" — net: %s %s", act.NetAmount.StringFixed(2), act.Currency)
				if act.Type != "" {
					baseRow.StatusMsg += " (" + act.Type + ")"
				}
			}
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
			baseRow.Status = qtStatusFlag
			baseRow.StatusMsg = "zero quantity — record manually"
			previewRows = append(previewRows, baseRow)
			continue
		}

		sec, secOK := secByTicker[act.Symbol]
		if !secOK {
			totFlag++
			baseRow.Status = qtStatusFlag
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
			baseRow.Status = qtStatusFlag
			baseRow.StatusMsg = "currency mismatch: activity is " + act.Currency + " but security is " + sec.Currency
			previewRows = append(previewRows, baseRow)
			continue
		}

		// Only CAD and USD are supported; flag anything else before it reaches commit.
		if act.Currency != "CAD" && act.Currency != "USD" {
			totFlag++
			baseRow.Status = qtStatusFlag
			baseRow.StatusMsg = "unsupported currency (" + act.Currency + ") — only CAD and USD securities can be imported"
			previewRows = append(previewRows, baseRow)
			continue
		}

		// Fetch BoC rate for display and to verify it's available before committing.
		var fxRateStr string
		if act.Currency == "USD" {
			fxRate, err := h.bocSvc.USDCADRate(ctx, act.TradeDate)
			if err != nil {
				totFlag++
				baseRow.Status = qtStatusFlag
				baseRow.StatusMsg = "BoC USD/CAD rate unavailable for " + act.TradeDate.Format(time.DateOnly) + ": " + err.Error()
				previewRows = append(previewRows, baseRow)
				continue
			}
			fxRateStr = fxRate.String()
		}

		totOK++
		baseRow.Status = qtStatusOK
		baseRow.TxType = string(txType)
		baseRow.AccountName = pAccountName
		baseRow.Quantity = qty.String()
		baseRow.Price = price.StringFixed(4)
		baseRow.FXRate = fxRateStr
		baseRow.Commission = comm.StringFixed(2)
		if fxRateStr != "" {
			fxRate, _ := decimal.NewFromString(fxRateStr)
			baseRow.PriceCAD = price.Mul(fxRate).StringFixed(4)
			baseRow.CommCAD = comm.Mul(fxRate).StringFixed(2)
		}
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
	switch strings.TrimSpace(a.Action) {
	case "Buy":
		return qtImport, "", transaction.TypeBuy
	case "Sell":
		return qtImport, "", transaction.TypeSell
	case "DIV", "INT":
		return qtImport, "", transaction.TypeDividend
	case "REI":
		// Dividend reinvestment: treated as a buy (acquires shares, increases ACB)
		return qtImport, "", transaction.TypeBuy
	case "CON", "WDR", "DEP", "TFI", "TFO", "EXP", "BRW", "":
		return qtSkip, "", ""
	case "FXT":
		// Norbert's Gambit journal: positive qty = receiving leg (e.g. DLR.TO acquired),
		// negative qty = sending leg (e.g. DLR.U.TO disposed). Zero qty can't be processed.
		if a.Quantity.IsPositive() {
			return qtImport, "", transaction.TypeJournal
		}
		if a.Quantity.IsNegative() {
			return qtImport, "", transaction.TypeTransferOut
		}
		return qtFlag, "FX conversion — zero quantity; enter manually", ""
	default:
		return qtFlag, "unknown action: " + a.Action, ""
	}
}

// validQTTypes restricts commit to types the Questrade preview can actually produce.
var validQTTypes = map[transaction.Type]bool{
	transaction.TypeBuy:         true,
	transaction.TypeSell:        true,
	transaction.TypeDividend:    true,
	transaction.TypeJournal:     true,
	transaction.TypeTransferOut: true,
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
		if !validQTTypes[txType] {
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

	existing, err := h.accounts.ListByUser(ctx, h.userID)
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
			UserID:        h.userID,
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

