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
