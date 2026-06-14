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

func TestTransactionStore_LinkNorbertGambitPairDirect(t *testing.T) {
	db := newTestDB(t)
	accounts := sqlite.NewAccountStore(db)
	securities := sqlite.NewSecurityStore(db)
	txStore := sqlite.NewTransactionStore(db)
	ctx := context.Background()

	cashAcct := &account.Account{UserID: 1, Name: "Cash", Type: account.TypeCash, Broker: "QT", Currency: "CAD"}
	if err := accounts.Create(ctx, cashAcct); err != nil {
		t.Fatalf("create account: %v", err)
	}
	dlru := &security.Security{Ticker: "DLR.U.TO", Exchange: "TSX", Name: "DLR.U.TO", Type: security.TypeETF, Currency: "USD"}
	dlr := &security.Security{Ticker: "DLR.TO", Exchange: "TSX", Name: "DLR.TO", Type: security.TypeETF, Currency: "CAD"}
	for _, s := range []*security.Security{dlru, dlr} {
		if err := securities.Create(ctx, s); err != nil {
			t.Fatalf("create security: %v", err)
		}
	}

	buyDate := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	sellDate := time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC)
	fxRate := decimal.NewFromFloat(1.44)
	give := &transaction.Transaction{
		AccountID: cashAcct.ID, SecurityID: dlru.ID, Type: transaction.TypeBuy,
		TradeDate: buyDate, SettledDate: buyDate,
		Quantity:      decimal.NewFromInt(13000),
		PriceNative:   decimal.NewFromFloat(9.85),
		PriceCAD:      decimal.NewFromFloat(14.18),
		FXRate:        &fxRate,
		Source:        transaction.SourceQuestrade,
	}
	recv := &transaction.Transaction{
		AccountID: cashAcct.ID, SecurityID: dlr.ID, Type: transaction.TypeSell,
		TradeDate: sellDate, SettledDate: sellDate,
		Quantity:    decimal.NewFromInt(13000),
		PriceNative: decimal.NewFromFloat(14.20),
		PriceCAD:    decimal.NewFromFloat(14.20),
		Source:      transaction.SourceQuestrade,
	}
	for _, tx := range []*transaction.Transaction{give, recv} {
		if err := txStore.Create(ctx, tx); err != nil {
			t.Fatalf("create tx: %v", err)
		}
	}

	if err := txStore.LinkNorbertGambitPairDirect(ctx, give, recv); err != nil {
		t.Fatalf("LinkNorbertGambitPairDirect: %v", err)
	}

	// Give (TypeBuy) must be linked to the synthetic fx_conversion.
	gotGive, err := txStore.GetByID(ctx, give.ID)
	if err != nil {
		t.Fatalf("GetByID give: %v", err)
	}
	if gotGive.Type != transaction.TypeBuy {
		t.Errorf("give type changed: got %s want buy", gotGive.Type)
	}
	if gotGive.LinkedTransactionID == nil {
		t.Fatal("give LinkedTransactionID is nil")
	}
	fxConvID := *gotGive.LinkedTransactionID

	// Receive (TypeSell) must be linked to the synthetic journal.
	gotRecv, err := txStore.GetByID(ctx, recv.ID)
	if err != nil {
		t.Fatalf("GetByID recv: %v", err)
	}
	if gotRecv.Type != transaction.TypeSell {
		t.Errorf("recv type changed: got %s want sell", gotRecv.Type)
	}
	if gotRecv.LinkedTransactionID == nil {
		t.Fatal("recv LinkedTransactionID is nil")
	}
	journalID := *gotRecv.LinkedTransactionID

	// Synthetic fx_conversion must be on give security, dated at give.TradeDate.
	fxConv, err := txStore.GetByID(ctx, fxConvID)
	if err != nil {
		t.Fatalf("GetByID fxConv: %v", err)
	}
	if fxConv.Type != transaction.TypeFXConversion {
		t.Errorf("synthetic type: got %s want fx_conversion", fxConv.Type)
	}
	if fxConv.SecurityID != dlru.ID {
		t.Errorf("fxConv securityID: got %d want %d", fxConv.SecurityID, dlru.ID)
	}
	if !fxConv.TradeDate.Equal(buyDate) {
		t.Errorf("fxConv TradeDate: got %s want %s", fxConv.TradeDate, buyDate)
	}
	if !fxConv.Quantity.Equal(give.Quantity) {
		t.Errorf("fxConv quantity: got %s want %s", fxConv.Quantity, give.Quantity)
	}
	// fxConv must point back at journal (cross-link).
	if fxConv.LinkedTransactionID == nil || *fxConv.LinkedTransactionID != journalID {
		t.Errorf("fxConv linked: got %v want %d", fxConv.LinkedTransactionID, journalID)
	}

	// Synthetic journal must be on receive security, dated at give.TradeDate.
	journal, err := txStore.GetByID(ctx, journalID)
	if err != nil {
		t.Fatalf("GetByID journal: %v", err)
	}
	if journal.Type != transaction.TypeJournal {
		t.Errorf("journal type: got %s want journal", journal.Type)
	}
	if journal.SecurityID != dlr.ID {
		t.Errorf("journal securityID: got %d want %d", journal.SecurityID, dlr.ID)
	}
	if !journal.TradeDate.Equal(buyDate) {
		t.Errorf("journal TradeDate: got %s want %s", journal.TradeDate, buyDate)
	}
	if !journal.Quantity.Equal(give.Quantity) {
		t.Errorf("journal quantity: got %s want %s", journal.Quantity, give.Quantity)
	}
	if !journal.PriceCAD.Equal(give.PriceCAD) {
		t.Errorf("journal PriceCAD: got %s want %s", journal.PriceCAD, give.PriceCAD)
	}
	// journal must point back at fxConv (cross-link).
	if journal.LinkedTransactionID == nil || *journal.LinkedTransactionID != fxConvID {
		t.Errorf("journal linked: got %v want %d", journal.LinkedTransactionID, fxConvID)
	}

	// Both original transactions must now be excluded from unlinked queries.
	unlinkedGives, _ := txStore.ListUnlinkedBySecurityAndType(ctx, dlru.ID, 1, transaction.TypeBuy)
	if len(unlinkedGives) != 0 {
		t.Errorf("give still appears as unlinked: %d", len(unlinkedGives))
	}
	unlinkedRecvs, _ := txStore.ListUnlinkedBySecurityAndType(ctx, dlr.ID, 1, transaction.TypeSell)
	if len(unlinkedRecvs) != 0 {
		t.Errorf("recv still appears as unlinked: %d", len(unlinkedRecvs))
	}
}

func ptrInt64(n int64) *int64 { return &n }
