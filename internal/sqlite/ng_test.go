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

func TestTransactionStore_ListUnlinkedBySecurityAndType(t *testing.T) {
	db := newTestDB(t)
	accounts := sqlite.NewAccountStore(db)
	securities := sqlite.NewSecurityStore(db)
	txStore := sqlite.NewTransactionStore(db)
	ctx := context.Background()

	margin := &account.Account{UserID: 1, Name: "Margin", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	other := &account.Account{UserID: 2, Name: "Other", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	for _, a := range []*account.Account{margin, other} {
		if err := accounts.Create(ctx, a); err != nil {
			t.Fatalf("create account: %v", err)
		}
	}

	dlr := &security.Security{Ticker: "DLR", Exchange: "TSX", Name: "DLR", Type: security.TypeETF, Currency: "CAD"}
	if err := securities.Create(ctx, dlr); err != nil {
		t.Fatalf("create security: %v", err)
	}

	date := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	seed := func(accID int64, typ transaction.Type, linked *int64) *transaction.Transaction {
		tx := &transaction.Transaction{
			AccountID: accID, SecurityID: dlr.ID, Type: typ,
			TradeDate: date, SettledDate: date,
			Quantity: decimal.NewFromInt(100), PriceCAD: decimal.NewFromInt(10),
			Source: transaction.SourceManual, LinkedTransactionID: linked,
		}
		if err := txStore.Create(ctx, tx); err != nil {
			t.Fatalf("seed tx: %v", err)
		}
		return tx
	}

	unlinked := seed(margin.ID, transaction.TypeTransferOut, nil)         // should appear
	seed(margin.ID, transaction.TypeTransferOut, ptrInt64(unlinked.ID))   // already linked — excluded
	seed(margin.ID, transaction.TypeJournal, nil)                         // wrong type — excluded
	seed(other.ID, transaction.TypeTransferOut, nil)                      // wrong user — excluded

	got, err := txStore.ListUnlinkedBySecurityAndType(ctx, dlr.ID, 1, transaction.TypeTransferOut)
	if err != nil {
		t.Fatalf("ListUnlinkedBySecurityAndType: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 result, got %d", len(got))
	}
	if got[0].ID != unlinked.ID {
		t.Errorf("expected tx ID=%d, got %d", unlinked.ID, got[0].ID)
	}
}

func TestTransactionStore_LinkNorbertGambitPair(t *testing.T) {
	db := newTestDB(t)
	accounts := sqlite.NewAccountStore(db)
	securities := sqlite.NewSecurityStore(db)
	txStore := sqlite.NewTransactionStore(db)
	ctx := context.Background()

	margin := &account.Account{UserID: 1, Name: "Margin", Type: account.TypeMargin, Broker: "B", Currency: "CAD"}
	if err := accounts.Create(ctx, margin); err != nil {
		t.Fatalf("create account: %v", err)
	}
	dlr := &security.Security{Ticker: "DLR", Exchange: "TSX", Name: "DLR", Type: security.TypeETF, Currency: "CAD"}
	if err := securities.Create(ctx, dlr); err != nil {
		t.Fatalf("create security: %v", err)
	}

	date := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	give := &transaction.Transaction{
		AccountID: margin.ID, SecurityID: dlr.ID, Type: transaction.TypeTransferOut,
		TradeDate: date, SettledDate: date,
		Quantity: decimal.NewFromInt(100), PriceCAD: decimal.NewFromInt(10),
		Source: transaction.SourceManual,
	}
	recv := &transaction.Transaction{
		AccountID: margin.ID, SecurityID: dlr.ID, Type: transaction.TypeJournal,
		TradeDate: date, SettledDate: date,
		Quantity: decimal.NewFromInt(100), PriceCAD: decimal.NewFromInt(10),
		Source: transaction.SourceManual,
	}
	for _, tx := range []*transaction.Transaction{give, recv} {
		if err := txStore.Create(ctx, tx); err != nil {
			t.Fatalf("create tx: %v", err)
		}
	}

	if err := txStore.LinkNorbertGambitPair(ctx, give.ID, recv.ID); err != nil {
		t.Fatalf("LinkNorbertGambitPair: %v", err)
	}

	gotGive, err := txStore.GetByID(ctx, give.ID)
	if err != nil {
		t.Fatalf("GetByID give: %v", err)
	}
	if gotGive.Type != transaction.TypeFXConversion {
		t.Errorf("give-leg type: got %s want fx_conversion", gotGive.Type)
	}
	if gotGive.LinkedTransactionID == nil || *gotGive.LinkedTransactionID != recv.ID {
		t.Errorf("give-leg linked_transaction_id: got %v want %d", gotGive.LinkedTransactionID, recv.ID)
	}

	gotRecv, err := txStore.GetByID(ctx, recv.ID)
	if err != nil {
		t.Fatalf("GetByID recv: %v", err)
	}
	if gotRecv.LinkedTransactionID == nil || *gotRecv.LinkedTransactionID != give.ID {
		t.Errorf("recv-leg linked_transaction_id: got %v want %d", gotRecv.LinkedTransactionID, give.ID)
	}
	if gotRecv.Type != transaction.TypeJournal {
		t.Errorf("recv-leg type should remain journal, got %s", gotRecv.Type)
	}
}

func ptrInt64(n int64) *int64 { return &n }
