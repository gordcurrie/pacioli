package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/broker"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type importPageData struct {
	Brokers []string
	Error   string
}

type importPreviewRow struct {
	Line         int
	Status       string // "ok" | "flag"
	StatusMsg    string
	TxType       string
	TradeDate    string
	SettledDate  string
	SecurityName string
	Ticker       string
	AccountNo    string
	AccountName  string
	Quantity     string
	Price        string
	Commission   string
}

type importPreviewData struct {
	BrokerName string
	Rows       []importPreviewRow
	CommitJSON string
	TotalOK    int
	TotalSkip  int
	TotalFlag  int
	Error      string
}

func (h *Handler) importPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "import", importPageData{Brokers: brokerNames()})
}

func (h *Handler) importPreview(w http.ResponseWriter, r *http.Request) {
	renderUpload := func(errMsg string) {
		h.render(w, "import", importPageData{Brokers: brokerNames(), Error: errMsg})
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		renderUpload("invalid form upload (max 10 MB)")
		return
	}

	brokerName := r.FormValue("broker")
	profile := broker.ByName(brokerName)
	if profile == nil {
		renderUpload("unknown broker profile")
		return
	}

	file, _, err := r.FormFile("csv_file")
	if err != nil {
		renderUpload("no file uploaded")
		return
	}
	defer func() { _ = file.Close() }()

	parsed, err := broker.ParseCSV(file, profile)
	if err != nil {
		renderUpload("failed to parse CSV: " + err.Error())
		return
	}

	accounts, err := h.accounts.ListByUser(r.Context(), h.userID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	type acctRef struct {
		ID   int64
		Name string
	}
	accountByNo := make(map[string]acctRef, len(accounts))
	for _, a := range accounts {
		if a.AccountNumber != "" {
			accountByNo[a.AccountNumber] = acctRef{a.ID, a.Name}
		}
	}

	securities, err := h.securities.ListAll(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	type secRef struct {
		ID       int64
		Ticker   string
		Currency string
	}
	secByName := make(map[string]secRef, len(securities))
	for _, s := range securities {
		secByName[s.Name] = secRef{s.ID, s.Ticker, s.Currency}
	}

	var previewRows []importPreviewRow
	var commitRows []broker.CommitRow
	totOK, totSkip, totFlag := 0, 0, 0

	for _, row := range parsed {
		if row.Status == broker.RowSkip {
			totSkip++
			continue
		}
		if row.Status == broker.RowFlag {
			totFlag++
			previewRows = append(previewRows, importPreviewRow{
				Line:         row.Line,
				Status:       "flag",
				StatusMsg:    row.FlagReason,
				TradeDate:    importDate(row.TradeDate),
				SecurityName: row.SecurityName,
				AccountNo:    row.AccountNo,
			})
			continue
		}

		pr := importPreviewRow{
			Line:         row.Line,
			TxType:       string(row.TxType),
			TradeDate:    importDate(row.TradeDate),
			SettledDate:  importDate(row.SettledDate),
			SecurityName: row.SecurityName,
			AccountNo:    row.AccountNo,
			Quantity:     row.Quantity.String(),
			Price:        row.Price.StringFixed(4),
			Commission:   row.Commission.StringFixed(2),
		}

		acct, acctOK := accountByNo[row.AccountNo]
		if !acctOK {
			pr.Status = "flag"
			pr.StatusMsg = "account not found — add account with number: " + row.AccountNo
			totFlag++
			previewRows = append(previewRows, pr)
			continue
		}
		pr.AccountName = acct.Name

		sec, secOK := secByName[row.SecurityName]
		if !secOK {
			results, err := h.securities.Search(r.Context(), row.SecurityName)
			if err != nil {
				h.serverError(w, r, err)
				return
			}
			if len(results) == 1 {
				sec = secRef{results[0].ID, results[0].Ticker, results[0].Currency}
				secOK = true
			}
		}
		if !secOK {
			pr.Status = "flag"
			pr.StatusMsg = "security not found — add security with name: " + row.SecurityName
			totFlag++
			previewRows = append(previewRows, pr)
			continue
		}

		if sec.Currency != "CAD" {
			pr.Status = "flag"
			pr.StatusMsg = "non-CAD security (" + sec.Currency + ") — FX rate not in CSV; add transaction manually"
			totFlag++
			previewRows = append(previewRows, pr)
			continue
		}

		pr.Ticker = sec.Ticker
		pr.Status = "ok"
		totOK++
		previewRows = append(previewRows, pr)

		commitRows = append(commitRows, broker.CommitRow{
			TradeDate:   row.TradeDate.Format(time.DateOnly),
			SettledDate: row.SettledDate.Format(time.DateOnly),
			AccountID:   acct.ID,
			SecurityID:  sec.ID,
			TxType:      string(row.TxType),
			Quantity:    row.Quantity.String(),
			Price:       row.Price.String(),
			Commission:  row.Commission.String(),
			Notes:       row.Notes,
		})
	}

	commitJSON, err := json.Marshal(commitRows)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, "import_preview", importPreviewData{
		BrokerName: brokerName,
		Rows:       previewRows,
		CommitJSON: string(commitJSON),
		TotalOK:    totOK,
		TotalSkip:  totSkip,
		TotalFlag:  totFlag,
	})
}

var validImportTypes = map[transaction.Type]bool{
	transaction.TypeBuy:           true,
	transaction.TypeSell:          true,
	transaction.TypeDividend:      true,
	transaction.TypeROCAdjustment: true,
}

func (h *Handler) importCommit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	var commitRows []broker.CommitRow
	if err := json.Unmarshal([]byte(r.FormValue("commit_rows")), &commitRows); err != nil || len(commitRows) == 0 {
		http.Redirect(w, r, "/import", http.StatusSeeOther)
		return
	}

	// resolve broker profile for source attribution
	profile := broker.ByName(r.FormValue("broker"))
	if profile == nil {
		profile = broker.NewCanaccordProfile()
	}
	src := profile.Source()

	// load user's accounts for ownership check
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

	for i := range commitRows {
		cr := &commitRows[i]

		if !ownedAccounts[cr.AccountID] {
			loggerFromCtx(ctx).Warn("import: account not owned", "account_id", cr.AccountID)
			continue
		}

		txType := transaction.Type(cr.TxType)
		if !validImportTypes[txType] {
			loggerFromCtx(ctx).Warn("import: invalid tx type", "type", cr.TxType)
			continue
		}

		tradeDate, err := time.Parse(time.DateOnly, cr.TradeDate)
		if err != nil {
			loggerFromCtx(ctx).Error("import: parse trade date", "err", err)
			continue
		}
		settledDate, err := time.Parse(time.DateOnly, cr.SettledDate)
		if err != nil {
			settledDate = tradeDate
		}

		qty, err := decimal.NewFromString(cr.Quantity)
		if err != nil || !qty.IsPositive() {
			loggerFromCtx(ctx).Error("import: parse quantity", "val", cr.Quantity, "err", err)
			continue
		}
		price, err := decimal.NewFromString(cr.Price)
		if err != nil || price.IsNegative() {
			loggerFromCtx(ctx).Error("import: parse price", "val", cr.Price, "err", err)
			continue
		}
		comm, err := decimal.NewFromString(cr.Commission)
		if err != nil || comm.IsNegative() {
			loggerFromCtx(ctx).Error("import: parse commission", "val", cr.Commission, "err", err)
			continue
		}

		tx := &transaction.Transaction{
			AccountID:        cr.AccountID,
			SecurityID:       cr.SecurityID,
			Type:             txType,
			TradeDate:        tradeDate,
			SettledDate:      settledDate,
			Quantity:         qty,
			PriceNative:      price,
			CommissionNative: comm,
			PriceCAD:         price,
			CommissionCAD:    comm,
			Source:           transaction.Source(src),
			Notes:            cr.Notes,
		}

		if err := h.transactions.Create(ctx, tx); err != nil {
			loggerFromCtx(ctx).Error("import: create transaction", "err", err)
			continue
		}

		if err := h.audits.Log(ctx, &audit.Entry{
			UserID:     h.userID,
			Action:     audit.ActionCreate,
			EntityType: audit.EntityTransaction,
			EntityID:   tx.ID,
			Source:     src,
			ImportID:   importID,
		}); err != nil {
			loggerFromCtx(ctx).Error("import: audit log", "err", err)
		}
	}

	http.Redirect(w, r, "/transactions", http.StatusSeeOther)
}

func brokerNames() []string {
	profiles := broker.All()
	names := make([]string, len(profiles))
	for i, p := range profiles {
		names[i] = p.Name()
	}
	return names
}

func importDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.DateOnly)
}
