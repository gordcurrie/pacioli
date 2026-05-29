package account_test

import (
	"testing"

	"github.com/gordcurrie/pacioli/internal/account"
)

func TestTypeIsRegistered(t *testing.T) {
	tests := []struct {
		typ        account.Type
		registered bool
	}{
		{account.TypeMargin, false},
		{account.TypeCash, false},
		{account.TypeTFSA, true},
		{account.TypeRRSP, true},
		{account.TypeRESP, true},
		{account.TypeLRSP, true},
		{account.TypeSRSP, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.typ), func(t *testing.T) {
			if got := tt.typ.IsRegistered(); got != tt.registered {
				t.Errorf("Type(%q).IsRegistered() = %v, want %v", tt.typ, got, tt.registered)
			}
		})
	}
}
