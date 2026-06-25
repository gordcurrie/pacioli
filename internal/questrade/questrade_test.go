package questrade

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestNumToDecimal(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "0"},
		{"0", "0"},
		{"95.23", "95.23"},
		{"-9.99", "-9.99"},
		{"1000", "1000"},
	}
	for _, tc := range cases {
		got, err := numToDecimal(json.Number(tc.input))
		if err != nil {
			t.Errorf("numToDecimal(%q): %v", tc.input, err)
			continue
		}
		want, _ := decimal.NewFromString(tc.want)
		if !got.Equal(want) {
			t.Errorf("numToDecimal(%q) = %s, want %s", tc.input, got, want)
		}
	}
}

func TestParseActivity(t *testing.T) {
	a := &activityJSON{
		TradeDate:   "2026-05-28T00:00:00.000000-04:00",
		SettledDate: "2026-05-28T00:00:00.000000-04:00",
		Action:      "TFI",
		Symbol:      "AEM.TO",
		Description: "AGNICO EAGLE MINES LIMITED CANACCORD GENUITY CORP. ACCOUNT TRANSFER BOOK VALUE 9841.94",
		Currency:    "CAD",
		Quantity:    json.Number("37"),
		Price:       json.Number("0"),
		GrossAmount: json.Number("0"),
		Commission:  json.Number("0"),
		NetAmount:   json.Number("0"),
		Type:        "Transfers",
	}
	act, err := parseActivity(a)
	if err != nil {
		t.Fatalf("parseActivity: %v", err)
	}
	if act.Action != "TFI" {
		t.Errorf("Action: got %q want TFI", act.Action)
	}
	if act.Symbol != "AEM.TO" {
		t.Errorf("Symbol: got %q want AEM.TO", act.Symbol)
	}
	if act.Description != a.Description {
		t.Errorf("Description not propagated")
	}
	if !act.Quantity.Equal(decimal.NewFromInt(37)) {
		t.Errorf("Quantity: got %s want 37", act.Quantity)
	}
	if !act.Price.IsZero() {
		t.Errorf("Price: got %s want 0", act.Price)
	}
	if act.TradeDate.IsZero() {
		t.Error("TradeDate not parsed")
	}
}

func TestParseActivity_BadDate(t *testing.T) {
	a := &activityJSON{
		TradeDate:   "not-a-date",
		SettledDate: "2026-05-28T00:00:00.000000-04:00",
		Quantity:    json.Number("1"),
		Price:       json.Number("0"),
		GrossAmount: json.Number("0"),
		Commission:  json.Number("0"),
		NetAmount:   json.Number("0"),
	}
	_, err := parseActivity(a)
	if err == nil {
		t.Error("expected error for bad trade date")
	}
}

func TestParseActivity_SettledDateFallback(t *testing.T) {
	// bad settled date falls back to trade date
	a := &activityJSON{
		TradeDate:   "2026-05-28T00:00:00.000000-04:00",
		SettledDate: "bad",
		Quantity:    json.Number("10"),
		Price:       json.Number("5.50"),
		GrossAmount: json.Number("55"),
		Commission:  json.Number("0"),
		NetAmount:   json.Number("55"),
	}
	act, err := parseActivity(a)
	if err != nil {
		t.Fatalf("parseActivity: %v", err)
	}
	if !act.SettledDate.Equal(act.TradeDate) {
		t.Error("SettledDate should fall back to TradeDate on parse error")
	}
}

func TestParseActivity_BadNumericFields(t *testing.T) {
	validDate := "2026-05-28T00:00:00.000000-04:00"
	cases := []struct {
		name        string
		quantity    json.Number
		price       json.Number
		grossAmount json.Number
		commission  json.Number
		netAmount   json.Number
	}{
		{"bad quantity", "N/A", "0", "0", "0", "0"},
		{"bad price", "10", "N/A", "0", "0", "0"},
		{"bad gross", "10", "5", "N/A", "0", "0"},
		{"bad commission", "10", "5", "50", "N/A", "0"},
		{"bad net", "10", "5", "50", "0", "N/A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &activityJSON{
				TradeDate:   validDate,
				SettledDate: validDate,
				Quantity:    tc.quantity,
				Price:       tc.price,
				GrossAmount: tc.grossAmount,
				Commission:  tc.commission,
				NetAmount:   tc.netAmount,
			}
			_, err := parseActivity(a)
			if err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestTokenIsExpired(t *testing.T) {
	expired := Token{ExpiresAt: time.Now().Add(-time.Minute)}
	if !expired.IsExpired() {
		t.Error("past token should be expired")
	}
	fresh := Token{ExpiresAt: time.Now().Add(10 * time.Minute)}
	if fresh.IsExpired() {
		t.Error("future token should not be expired")
	}
	// within 60-second buffer: treated as expired
	soon := Token{ExpiresAt: time.Now().Add(30 * time.Second)}
	if !soon.IsExpired() {
		t.Error("token within 60s buffer should be expired")
	}
}
