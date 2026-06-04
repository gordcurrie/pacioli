package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/distribution"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

func TestTransactionStore_ListNonRegisteredDisposalsByUser(t *testing.T) {
	db := newTestDB(t)
	accounts := sqlite.NewAccountStore(db)
	securities := sqlite.NewSecurityStore(db)
	txStore := sqlite.NewTransactionStore(db)
	ctx := context.Background()

	margin := &account.Account{UserID: 1, Name: "Margin", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	tfsa := &account.Account{UserID: 1, Name: "TFSA", Type: account.TypeTFSA, Broker: "B", Currency: "CAD"}
	if err := accounts.Create(ctx, margin); err != nil {
		t.Fatalf("create margin: %v", err)
	}
	if err := accounts.Create(ctx, tfsa); err != nil {
		t.Fatalf("create tfsa: %v", err)
	}

	sec := &security.Security{Ticker: "XYZ", Exchange: "TSX", Name: "XYZ", Type: security.TypeEquity, Currency: "CAD"}
	if err := securities.Create(ctx, sec); err != nil {
		t.Fatalf("create security: %v", err)
	}

	inYear := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	outOfYear := time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC)

	seed := func(accID int64, typ transaction.Type, date time.Time) {
		tx := &transaction.Transaction{
			AccountID: accID, SecurityID: sec.ID, Type: typ,
			TradeDate: date, SettledDate: date,
			Quantity: decimal.NewFromInt(10), PriceCAD: decimal.NewFromFloat(15),
			Source: transaction.SourceManual,
		}
		if err := txStore.Create(ctx, tx); err != nil {
			t.Fatalf("seed tx: %v", err)
		}
	}

	seed(margin.ID, transaction.TypeSell, inYear)     // should appear
	seed(margin.ID, transaction.TypeBuy, inYear)      // wrong type
	seed(tfsa.ID, transaction.TypeSell, inYear)       // wrong account type
	seed(margin.ID, transaction.TypeSell, outOfYear)  // wrong year

	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)

	got, err := txStore.ListNonRegisteredDisposalsByUser(ctx, 1, from, to)
	if err != nil {
		t.Fatalf("ListNonRegisteredDisposalsByUser: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 result, got %d", len(got))
	}
	if got[0].AccountID != margin.ID {
		t.Errorf("expected margin account, got accountID=%d", got[0].AccountID)
	}
}

func TestTransactionStore_ListBySecurityAllAccounts(t *testing.T) {
	db := newTestDB(t)
	accounts := sqlite.NewAccountStore(db)
	securities := sqlite.NewSecurityStore(db)
	txStore := sqlite.NewTransactionStore(db)
	ctx := context.Background()

	margin := &account.Account{UserID: 1, Name: "Margin", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	tfsa := &account.Account{UserID: 1, Name: "TFSA", Type: account.TypeTFSA, Broker: "B", Currency: "CAD"}
	other := &account.Account{UserID: 2, Name: "Other", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	for _, acc := range []*account.Account{margin, tfsa, other} {
		if err := accounts.Create(ctx, acc); err != nil {
			t.Fatalf("create account: %v", err)
		}
	}

	sec := &security.Security{Ticker: "ABC", Exchange: "TSX", Name: "ABC", Type: security.TypeEquity, Currency: "CAD"}
	if err := securities.Create(ctx, sec); err != nil {
		t.Fatalf("create security: %v", err)
	}

	tradeDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	seed := func(accID int64) {
		tx := &transaction.Transaction{
			AccountID: accID, SecurityID: sec.ID, Type: transaction.TypeBuy,
			TradeDate: tradeDate, SettledDate: tradeDate,
			Quantity: decimal.NewFromInt(10), PriceCAD: decimal.NewFromFloat(10),
			Source: transaction.SourceManual,
		}
		if err := txStore.Create(ctx, tx); err != nil {
			t.Fatalf("seed tx: %v", err)
		}
	}

	seed(margin.ID) // user 1 — should appear
	seed(tfsa.ID)   // user 1 registered — should appear (all accounts)
	seed(other.ID)  // user 2 — should NOT appear

	got, err := txStore.ListBySecurityAllAccounts(ctx, sec.ID, 1)
	if err != nil {
		t.Fatalf("ListBySecurityAllAccounts: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 results (non-reg + registered for user 1), got %d", len(got))
	}
	for _, tx := range got {
		if tx.AccountID == other.ID {
			t.Error("user 2 transaction should not appear")
		}
	}
}

func TestDistributionStore_ListByTaxYear(t *testing.T) {
	db := newTestDB(t)
	securities := sqlite.NewSecurityStore(db)
	store := sqlite.NewDistributionStore(db)
	ctx := context.Background()

	sec1 := &security.Security{Ticker: "ZWC", Exchange: "TSX", Name: "ZWC", Type: security.TypeETF, Currency: "CAD"}
	sec2 := &security.Security{Ticker: "ZWB", Exchange: "TSX", Name: "ZWB", Type: security.TypeETF, Currency: "CAD"}
	for _, s := range []*security.Security{sec1, sec2} {
		if err := securities.Create(ctx, s); err != nil {
			t.Fatalf("create security: %v", err)
		}
	}

	_ = store.Upsert(ctx, &distribution.Distribution{SecurityID: sec1.ID, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.12)})
	_ = store.Upsert(ctx, &distribution.Distribution{SecurityID: sec2.ID, TaxYear: 2023, ROCPerUnit: decimal.NewFromFloat(0.08)})
	_ = store.Upsert(ctx, &distribution.Distribution{SecurityID: sec1.ID, TaxYear: 2022, ROCPerUnit: decimal.NewFromFloat(0.10)})

	got, err := store.ListByTaxYear(ctx, 2023)
	if err != nil {
		t.Fatalf("ListByTaxYear: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 distributions for 2023, got %d", len(got))
	}
	for _, d := range got {
		if d.TaxYear != 2023 {
			t.Errorf("expected tax_year=2023, got %d", d.TaxYear)
		}
	}

	// wrong year returns empty
	none, err := store.ListByTaxYear(ctx, 2020)
	if err != nil {
		t.Fatalf("ListByTaxYear (empty): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 results for 2020, got %d", len(none))
	}
}
