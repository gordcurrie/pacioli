package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

func TestTransactionStore(t *testing.T) {
	db := newTestDB(t)
	accounts := sqlite.NewAccountStore(db)
	securities := sqlite.NewSecurityStore(db)
	txStore := sqlite.NewTransactionStore(db)
	ctx := context.Background()

	// seed account and security
	acc := &account.Account{UserID: 1, Name: "Margin", Type: account.TypeMargin, Broker: "Questrade", Currency: "CAD"}
	if err := accounts.Create(ctx, acc); err != nil {
		t.Fatalf("create account: %v", err)
	}
	sec := &security.Security{Ticker: "XEQT", Exchange: "TSX", Name: "iShares XEQT", Type: security.TypeETF, Currency: "CAD"}
	if err := securities.Create(ctx, sec); err != nil {
		t.Fatalf("create security: %v", err)
	}

	tradeDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	t.Run("create and get", func(t *testing.T) {
		tx := &transaction.Transaction{
			AccountID:        acc.ID,
			SecurityID:       sec.ID,
			Type:             transaction.TypeBuy,
			TradeDate:        tradeDate,
			SettledDate:      tradeDate.AddDate(0, 0, 2),
			Quantity:         decimal.NewFromInt(100),
			PriceNative:      decimal.NewFromFloat(25.50),
			CommissionNative: decimal.NewFromFloat(4.95),
			PriceCAD:         decimal.NewFromFloat(25.50),
			CommissionCAD:    decimal.NewFromFloat(4.95),
			Source:           transaction.SourceManual,
		}

		if err := txStore.Create(ctx, tx); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if tx.ID == 0 {
			t.Fatal("expected non-zero ID after create")
		}

		got, err := txStore.GetByID(ctx, tx.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if !got.Quantity.Equal(tx.Quantity) {
			t.Errorf("Quantity = %s, want %s", got.Quantity, tx.Quantity)
		}
		if !got.PriceNative.Equal(tx.PriceNative) {
			t.Errorf("PriceNative = %s, want %s", got.PriceNative, tx.PriceNative)
		}
	})

	t.Run("list by account", func(t *testing.T) {
		txs, err := txStore.ListByAccount(ctx, acc.ID)
		if err != nil {
			t.Fatalf("ListByAccount: %v", err)
		}
		if len(txs) == 0 {
			t.Error("expected at least one transaction")
		}
	})

	t.Run("list by security non-registered pools correctly", func(t *testing.T) {
		// non-registered account — should appear in ACB pool
		marginAcc := &account.Account{UserID: 1, Name: "Cash", Type: account.TypeCash, Broker: "Questrade", Currency: "CAD"}
		if err := accounts.Create(ctx, marginAcc); err != nil {
			t.Fatalf("create cash account: %v", err)
		}
		// registered account — should NOT appear in ACB pool
		tfsaAcc := &account.Account{UserID: 1, Name: "TFSA", Type: account.TypeTFSA, Broker: "Questrade", Currency: "CAD"}
		if err := accounts.Create(ctx, tfsaAcc); err != nil {
			t.Fatalf("create TFSA: %v", err)
		}

		txInTFSA := &transaction.Transaction{
			AccountID: tfsaAcc.ID, SecurityID: sec.ID,
			Type: transaction.TypeBuy, TradeDate: tradeDate, SettledDate: tradeDate,
			Quantity: decimal.NewFromInt(50), PriceNative: decimal.NewFromFloat(25.50),
			PriceCAD: decimal.NewFromFloat(25.50), Source: transaction.SourceManual,
		}
		if err := txStore.Create(ctx, txInTFSA); err != nil {
			t.Fatalf("create TFSA tx: %v", err)
		}

		pooled, err := txStore.ListBySecurityNonRegistered(ctx, sec.ID, 1)
		if err != nil {
			t.Fatalf("ListBySecurityNonRegistered: %v", err)
		}
		for _, tx := range pooled {
			if tx.AccountID == tfsaAcc.ID {
				t.Error("TFSA transaction should not appear in non-registered pool")
			}
		}
	})

	t.Run("list by date range", func(t *testing.T) {
		from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
		txs, err := txStore.ListByDateRange(ctx, acc.ID, from, to)
		if err != nil {
			t.Fatalf("ListByDateRange: %v", err)
		}
		if len(txs) == 0 {
			t.Error("expected transactions in date range")
		}
	})

	t.Run("UpdateFXRate persists and is readable", func(t *testing.T) {
		usdSec := &security.Security{Ticker: "WMT", Exchange: "NYSE", Name: "Walmart", Type: security.TypeEquity, Currency: "USD"}
		if err := securities.Create(ctx, usdSec); err != nil {
			t.Fatalf("create USD security: %v", err)
		}

		origFX := decimal.NewFromFloat(1.25)
		tx := &transaction.Transaction{
			AccountID:        acc.ID,
			SecurityID:       usdSec.ID,
			Type:             transaction.TypeBuy,
			TradeDate:        tradeDate,
			SettledDate:      tradeDate.AddDate(0, 0, 2),
			Quantity:         decimal.NewFromInt(10),
			PriceNative:      decimal.NewFromFloat(50.00),
			CommissionNative: decimal.NewFromFloat(1.00),
			FXRate:           &origFX,
			PriceCAD:         decimal.NewFromFloat(62.50),
			CommissionCAD:    decimal.NewFromFloat(1.25),
			Source:           transaction.SourceQuestrade,
		}
		if err := txStore.Create(ctx, tx); err != nil {
			t.Fatalf("Create USD tx: %v", err)
		}

		newFX := decimal.NewFromFloat(1.38)
		newPriceCAD := tx.PriceNative.Mul(newFX)
		newCommCAD := tx.CommissionNative.Mul(newFX)

		if err := txStore.UpdateFXRate(ctx, tx.ID, &newFX, newPriceCAD, newCommCAD); err != nil {
			t.Fatalf("UpdateFXRate: %v", err)
		}

		got, err := txStore.GetByID(ctx, tx.ID)
		if err != nil {
			t.Fatalf("GetByID after UpdateFXRate: %v", err)
		}
		if got.FXRate == nil {
			t.Fatal("FXRate is nil after update")
		}
		if !got.FXRate.Equal(newFX) {
			t.Errorf("FXRate = %s, want %s", got.FXRate, newFX)
		}
		if !got.PriceCAD.Equal(newPriceCAD) {
			t.Errorf("PriceCAD = %s, want %s", got.PriceCAD, newPriceCAD)
		}
		if !got.CommissionCAD.Equal(newCommCAD) {
			t.Errorf("CommissionCAD = %s, want %s", got.CommissionCAD, newCommCAD)
		}
	})
}
