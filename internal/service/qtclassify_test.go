package service_test

import (
	"testing"

	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

func TestClassifyActivity_Buy(t *testing.T) {
	a := &questrade.Activity{Action: "Buy", Quantity: decimal.NewFromInt(10)}
	status, msg, txType := service.ClassifyQTActivity(a)
	if status != service.QTActivityImport {
		t.Errorf("status: got %v want Import", status)
	}
	if msg != "" {
		t.Errorf("msg: got %q want empty", msg)
	}
	if txType != transaction.TypeBuy {
		t.Errorf("txType: got %v want Buy", txType)
	}
}

func TestClassifyActivity_Sell(t *testing.T) {
	a := &questrade.Activity{Action: "Sell", Quantity: decimal.NewFromInt(5)}
	status, _, txType := service.ClassifyQTActivity(a)
	if status != service.QTActivityImport {
		t.Errorf("status: got %v want Import", status)
	}
	if txType != transaction.TypeSell {
		t.Errorf("txType: got %v want Sell", txType)
	}
}

func TestClassifyActivity_Dividend(t *testing.T) {
	for _, action := range []string{"DIV", "INT"} {
		a := &questrade.Activity{Action: action}
		status, _, txType := service.ClassifyQTActivity(a)
		if status != service.QTActivityImport {
			t.Errorf("action=%s: status got %v want Import", action, status)
		}
		if txType != transaction.TypeDividend {
			t.Errorf("action=%s: txType got %v want Dividend", action, txType)
		}
	}
}

func TestClassifyActivity_REI_IsBuy(t *testing.T) {
	for _, action := range []string{"REI", "DRI"} {
		a := &questrade.Activity{Action: action, Quantity: decimal.NewFromInt(3)}
		status, _, txType := service.ClassifyQTActivity(a)
		if status != service.QTActivityImport {
			t.Errorf("action=%s: status got %v want Import", action, status)
		}
		if txType != transaction.TypeBuy {
			t.Errorf("action=%s: txType got %v want Buy (reinvestment)", action, txType)
		}
	}
}

func TestClassifyActivity_TFI_InKind_IsTransferIn(t *testing.T) {
	a := &questrade.Activity{Action: "TFI", Quantity: decimal.NewFromInt(100), Price: decimal.NewFromFloat(48.50)}
	status, _, txType := service.ClassifyQTActivity(a)
	if status != service.QTActivityImport {
		t.Errorf("status: got %v want Import", status)
	}
	if txType != transaction.TypeTransferIn {
		t.Errorf("txType: got %v want TransferIn", txType)
	}
}

func TestClassifyActivity_TFI_ZeroQty_IsSkip(t *testing.T) {
	a := &questrade.Activity{Action: "TFI", Quantity: decimal.Zero}
	status, _, _ := service.ClassifyQTActivity(a)
	if status != service.QTActivitySkip {
		t.Errorf("status: got %v want Skip", status)
	}
}

func TestClassifyActivity_TFI_AtPriceInDescription_IsTransferIn(t *testing.T) {
	a := &questrade.Activity{
		Action:      "TFI",
		Quantity:    decimal.NewFromFloat(1428.5714),
		Price:       decimal.Zero,
		Description: "AVENUE LIVING REAL ESTATE CORE TRUST CLASS W (400W) EXTERNAL TRANSFER-IN AS OF 05/28/26 @  14.76000",
	}
	status, _, txType := service.ClassifyQTActivity(a)
	if status != service.QTActivityImport {
		t.Errorf("status: got %v want Import", status)
	}
	if txType != transaction.TypeTransferIn {
		t.Errorf("txType: got %v want TransferIn", txType)
	}
	if a.Price.IsZero() {
		t.Error("price should have been set from @ price in description")
	}
	want, _ := decimal.NewFromString("14.76000")
	if !a.Price.Equal(want) {
		t.Errorf("price: got %s want 14.76000", a.Price)
	}
}

func TestClassifyActivity_TFI_AtPriceNotFalseMatchOnAccountRef(t *testing.T) {
	// "@" in an account number should NOT be parsed as a price
	a := &questrade.Activity{
		Action:      "TFI",
		Quantity:    decimal.NewFromInt(100),
		Price:       decimal.Zero,
		Description: "SOME STOCK CANACCORD GENUITY CORP. ACCOUNT TRANSFER ACCOUNT(S): 374Y0PE1 NO PRICE HERE",
	}
	status, msg, _ := service.ClassifyQTActivity(a)
	if status != service.QTActivityFlag {
		t.Errorf("status: got %v want Flag — no valid price in description", status)
	}
	if msg == "" {
		t.Error("expected flag message when description has no extractable price")
	}
}

func TestClassifyActivity_TFI_BookValueInDescription_IsTransferIn(t *testing.T) {
	a := &questrade.Activity{
		Action:      "TFI",
		Quantity:    decimal.NewFromInt(100),
		Price:       decimal.Zero,
		Description: "SOME STOCK CANACCORD GENUITY CORP. ACCOUNT TRANSFER BOOK VALUE            9841.94",
	}
	status, _, txType := service.ClassifyQTActivity(a)
	if status != service.QTActivityImport {
		t.Errorf("status: got %v want Import", status)
	}
	if txType != transaction.TypeTransferIn {
		t.Errorf("txType: got %v want TransferIn", txType)
	}
	// price = 9841.94 / 100 = 98.4194
	want, _ := decimal.NewFromString("98.4194")
	if !a.Price.Equal(want) {
		t.Errorf("price: got %s want %s (book value ÷ qty)", a.Price, want)
	}
}

func TestClassifyActivity_TFI_NoBookValue_IsFlag(t *testing.T) {
	a := &questrade.Activity{Action: "TFI", Quantity: decimal.NewFromInt(100), Price: decimal.Zero, Description: "SOME STOCK CANACCORD CAPITAL CORP. TRANSFER IN"}
	status, msg, _ := service.ClassifyQTActivity(a)
	if status != service.QTActivityFlag {
		t.Errorf("status: got %v want Flag", status)
	}
	if msg == "" {
		t.Error("expected non-empty flag message for zero-price TFI")
	}
}

func TestClassifyActivity_Skip(t *testing.T) {
	for _, action := range []string{"CON", "WDR", "DEP", "TFO", "EXP", "BRW", "LFJ", ""} {
		a := &questrade.Activity{Action: action}
		status, _, _ := service.ClassifyQTActivity(a)
		if status != service.QTActivitySkip {
			t.Errorf("action=%q: status got %v want Skip", action, status)
		}
	}
}

func TestClassifyActivity_FXT_Positive_IsJournal(t *testing.T) {
	a := &questrade.Activity{Action: "FXT", Quantity: decimal.NewFromInt(100)}
	status, _, txType := service.ClassifyQTActivity(a)
	if status != service.QTActivityImport {
		t.Errorf("status: got %v want Import", status)
	}
	if txType != transaction.TypeJournal {
		t.Errorf("txType: got %v want Journal", txType)
	}
}

func TestClassifyActivity_FXT_Negative_IsTransferOut(t *testing.T) {
	a := &questrade.Activity{Action: "FXT", Quantity: decimal.NewFromInt(-100)}
	status, _, txType := service.ClassifyQTActivity(a)
	if status != service.QTActivityImport {
		t.Errorf("status: got %v want Import", status)
	}
	if txType != transaction.TypeTransferOut {
		t.Errorf("txType: got %v want TransferOut", txType)
	}
}

func TestClassifyActivity_FXT_Zero_IsFlag(t *testing.T) {
	a := &questrade.Activity{Action: "FXT", Quantity: decimal.Zero, NetAmount: decimal.NewFromInt(50), Currency: "CAD"}
	status, msg, _ := service.ClassifyQTActivity(a)
	if status != service.QTActivityFlag {
		t.Errorf("status: got %v want Flag", status)
	}
	if msg == "" {
		t.Error("expected non-empty flag message for zero-quantity FXT")
	}
}

func TestClassifyActivity_FXT_Whitespace(t *testing.T) {
	a := &questrade.Activity{Action: " FXT ", Quantity: decimal.NewFromInt(10)}
	status, _, txType := service.ClassifyQTActivity(a)
	if status != service.QTActivityImport {
		t.Errorf("FXT with whitespace: status got %v want Import", status)
	}
	if txType != transaction.TypeJournal {
		t.Errorf("FXT with whitespace: txType got %v want Journal", txType)
	}
}

func TestClassifyActivity_Unknown_IsFlag(t *testing.T) {
	a := &questrade.Activity{Action: "BOGUS"}
	status, msg, _ := service.ClassifyQTActivity(a)
	if status != service.QTActivityFlag {
		t.Errorf("status: got %v want Flag", status)
	}
	if msg == "" {
		t.Error("expected non-empty flag message for unknown action")
	}
}
