package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
)

func TestYahooSymbol(t *testing.T) {
	cases := []struct {
		ticker, exchange, want string
	}{
		// TSX: no suffix yet
		{"SU", "TSX", "SU.TO"},
		// TSX: already has suffix
		{"CCO.TO", "TSX", "CCO.TO"},
		// TSX: share class dot → hyphen
		{"TECK.B.TO", "TSX", "TECK-B.TO"},
		{"TECK.B", "TSX", "TECK-B.TO"},
		// TSX-V
		{"XYZ.V", "TSX-V", "XYZ.V"},
		{"XYZ", "TSX-V", "XYZ.V"},
		{"TSXV", "TSXV", "TSXV.V"},
		// NEO
		{"ABC.NEO", "NEO", "ABC.NEO"},
		{"ABC", "NEO Exchange", "ABC.NEO"},
		// US exchanges — no suffix; dots become hyphens
		{"AAPL", "NASDAQ", "AAPL"},
		{"BRK.B", "NYSE", "BRK-B"},
		{"SU", "NYSE", "SU"},
	}
	for _, tc := range cases {
		got := service.YahooSymbol(tc.ticker, tc.exchange)
		if got != tc.want {
			t.Errorf("YahooSymbol(%q, %q) = %q, want %q", tc.ticker, tc.exchange, got, tc.want)
		}
	}
}

func TestYahooFetcher_FetchPrices(t *testing.T) {
	makeResponse := func(symbol, currency string, price float64) []byte {
		resp := map[string]any{
			"chart": map[string]any{
				"result": []any{
					map[string]any{
						"meta": map[string]any{
							"symbol":             symbol,
							"currency":           currency,
							"regularMarketPrice": price,
						},
					},
				},
				"error": nil,
			},
		}
		b, _ := json.Marshal(resp)
		return b
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve different prices based on path.
		switch r.URL.Path {
		case "/v8/finance/chart/CCO.TO":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(makeResponse("CCO.TO", "CAD", 152.19))
		case "/v8/finance/chart/FAIL.TO":
			http.Error(w, "not found", http.StatusNotFound)
		default:
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	fetcher := service.NewYahooFetcherWithClient(nil, &http.Client{}, srv.URL)

	secs := []*security.Security{
		{ID: 1, Ticker: "CCO.TO", Exchange: "TSX", Currency: "CAD"},
		{ID: 2, Ticker: "FAIL.TO", Exchange: "TSX", Currency: "CAD"},
	}

	results := fetcher.FetchPrices(context.Background(), secs)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// First result: success
	if results[0].Err != nil {
		t.Errorf("CCO.TO: unexpected error: %v", results[0].Err)
	}
	if results[0].PriceCAD.String() != "152.19" {
		t.Errorf("CCO.TO price: got %s, want 152.19", results[0].PriceCAD.String())
	}

	// Second result: failure, no panic
	if results[1].Err == nil {
		t.Error("FAIL.TO: expected error, got nil")
	}
}
