package service_test

import (
	"testing"

	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal {
	v, _ := decimal.NewFromString(s)
	return v
}

func TestCalculateACB_Buy(t *testing.T) {
	txs := []*transaction.Transaction{
		{Type: transaction.TypeBuy, Quantity: d("100"), PriceCAD: d("10.00"), CommissionCAD: d("4.95")},
	}
	r := service.CalculateACB(1, txs)
	if !r.Shares.Equal(d("100")) {
		t.Errorf("shares: got %s want 100", r.Shares)
	}
	// ACB = 100*10 + 4.95 = 1004.95
	if !r.TotalACB.Equal(d("1004.95")) {
		t.Errorf("total ACB: got %s want 1004.95", r.TotalACB)
	}
	if !r.ACBPerShare.Equal(d("10.0495")) {
		t.Errorf("ACB/share: got %s want 10.0495", r.ACBPerShare)
	}
}

func TestCalculateACB_BuyThenSell(t *testing.T) {
	txs := []*transaction.Transaction{
		{Type: transaction.TypeBuy, Quantity: d("100"), PriceCAD: d("10.00"), CommissionCAD: d("4.95")},
		{Type: transaction.TypeSell, Quantity: d("40"), PriceCAD: d("12.00"), CommissionCAD: d("4.95")},
	}
	r := service.CalculateACB(1, txs)
	// After buy: 100 shares, ACB=1004.95, ACB/share=10.0495
	// After sell 40: shares=60, ACB = 1004.95 - 40*10.0495 = 1004.95 - 401.98 = 602.97
	if !r.Shares.Equal(d("60")) {
		t.Errorf("shares: got %s want 60", r.Shares)
	}
	expected := d("1004.95").Sub(d("10.0495").Mul(d("40")))
	if !r.TotalACB.Equal(expected) {
		t.Errorf("total ACB: got %s want %s", r.TotalACB, expected)
	}
}

func TestCalculateACB_MultipleBuys_AverageCost(t *testing.T) {
	txs := []*transaction.Transaction{
		{Type: transaction.TypeBuy, Quantity: d("100"), PriceCAD: d("10.00"), CommissionCAD: d("0")},
		{Type: transaction.TypeBuy, Quantity: d("100"), PriceCAD: d("12.00"), CommissionCAD: d("0")},
	}
	r := service.CalculateACB(1, txs)
	// ACB = 100*10 + 100*12 = 2200; shares = 200; ACB/share = 11
	if !r.Shares.Equal(d("200")) {
		t.Errorf("shares: got %s want 200", r.Shares)
	}
	if !r.TotalACB.Equal(d("2200")) {
		t.Errorf("total ACB: got %s want 2200", r.TotalACB)
	}
	if !r.ACBPerShare.Equal(d("11")) {
		t.Errorf("ACB/share: got %s want 11", r.ACBPerShare)
	}
}

func TestCalculateACB_ROCAdjustment(t *testing.T) {
	txs := []*transaction.Transaction{
		{Type: transaction.TypeBuy, Quantity: d("100"), PriceCAD: d("10.00"), CommissionCAD: d("0")},
		// ROC: 100 shares × $0.50/unit = $50 reduction
		{Type: transaction.TypeROCAdjustment, Quantity: d("100"), PriceCAD: d("0.50"), CommissionCAD: d("0")},
	}
	r := service.CalculateACB(1, txs)
	// ACB = 1000 - 50 = 950
	if !r.TotalACB.Equal(d("950")) {
		t.Errorf("total ACB: got %s want 950", r.TotalACB)
	}
}

func TestCalculateACB_DividendNoEffect(t *testing.T) {
	txs := []*transaction.Transaction{
		{Type: transaction.TypeBuy, Quantity: d("100"), PriceCAD: d("10.00"), CommissionCAD: d("0")},
		{Type: transaction.TypeDividend, Quantity: d("100"), PriceCAD: d("0.25"), CommissionCAD: d("0")},
	}
	r := service.CalculateACB(1, txs)
	if !r.TotalACB.Equal(d("1000")) {
		t.Errorf("dividend should not affect ACB: got %s want 1000", r.TotalACB)
	}
}

func TestCalculateACB_ROCExceedsACB(t *testing.T) {
	txs := []*transaction.Transaction{
		{Type: transaction.TypeBuy, Quantity: d("100"), PriceCAD: d("10.00"), CommissionCAD: d("0")},
		// ROC of $20/unit far exceeds total ACB of $1000
		{Type: transaction.TypeROCAdjustment, Quantity: d("100"), PriceCAD: d("20.00"), CommissionCAD: d("0")},
	}
	r := service.CalculateACB(1, txs)
	if r.TotalACB.IsNegative() {
		t.Errorf("ACB should not go negative on excess ROC: got %s", r.TotalACB)
	}
	if !r.TotalACB.IsZero() {
		t.Errorf("ACB should be floored at 0 on excess ROC: got %s", r.TotalACB)
	}
}

func TestCalculateACB_Empty(t *testing.T) {
	r := service.CalculateACB(1, nil)
	if !r.Shares.IsZero() || !r.TotalACB.IsZero() {
		t.Error("empty transaction list should produce zero result")
	}
}
