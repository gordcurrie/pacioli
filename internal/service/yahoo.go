package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/shopspring/decimal"
)

// YahooFetcher retrieves market prices from the Yahoo Finance chart API.
// Prices are converted to CAD using the BOC noon rate.
type YahooFetcher struct {
	bocSvc  *BOCFetcher
	hc      *http.Client
	baseURL string // overridable for testing; defaults to https://query1.finance.yahoo.com
}

// NewYahooFetcher constructs a YahooFetcher.
func NewYahooFetcher(bocSvc *BOCFetcher) *YahooFetcher {
	return &YahooFetcher{
		bocSvc:  bocSvc,
		hc:      &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://query1.finance.yahoo.com",
	}
}

// NewYahooFetcherWithClient constructs a YahooFetcher with a custom HTTP client and base URL.
// Intended for testing only.
func NewYahooFetcherWithClient(bocSvc *BOCFetcher, hc *http.Client, baseURL string) *YahooFetcher {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &YahooFetcher{bocSvc: bocSvc, hc: hc, baseURL: baseURL}
}

// PriceFetchResult holds the result for one security fetch attempt.
type PriceFetchResult struct {
	SecurityID int64
	Ticker     string
	PriceCAD   decimal.Decimal
	Err        error
}

// FetchPrices fetches current market prices for the given securities and returns
// them in CAD. Securities whose price cannot be fetched are reported in the
// returned slice with a non-nil Err; they do not cause the whole call to fail.
func (f *YahooFetcher) FetchPrices(ctx context.Context, secs []*security.Security) []PriceFetchResult {
	if len(secs) == 0 {
		return nil
	}

	// Pre-fetch the USD/CAD rate before spawning goroutines so a transient BOC error
	// doesn't get permanently cached — each FetchPrices call gets a fresh attempt.
	var fxRate decimal.Decimal
	var fxErr error
	for _, s := range secs {
		if s.Currency == "USD" {
			if f.bocSvc == nil {
				fxErr = fmt.Errorf("yahoo: bocSvc not configured; cannot convert USD price")
			} else {
				fxRate, fxErr = f.bocSvc.USDCADRate(ctx, time.Now().UTC())
			}
			break
		}
	}

	sem := make(chan struct{}, 5) // max 5 concurrent Yahoo requests
	out := make([]PriceFetchResult, len(secs))

	var wg sync.WaitGroup
	for i, sec := range secs {
		wg.Add(1)
		go func(idx int, s *security.Security) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			sym := YahooSymbol(s.Ticker, s.Exchange)
			nativePrice, currency, err := f.fetchQuote(ctx, sym)
			if err != nil {
				out[idx] = PriceFetchResult{SecurityID: s.ID, Ticker: s.Ticker, Err: err}
				return
			}

			priceCAD := nativePrice
			switch currency {
			case "CAD":
				// already in CAD
			case "USD":
				if fxErr != nil {
					out[idx] = PriceFetchResult{SecurityID: s.ID, Ticker: s.Ticker, Err: fmt.Errorf("usd/cad rate: %w", fxErr)}
					return
				}
				priceCAD = nativePrice.Mul(fxRate)
			default:
				out[idx] = PriceFetchResult{SecurityID: s.ID, Ticker: s.Ticker, Err: fmt.Errorf("unsupported currency %q from Yahoo for %s", currency, sym)}
				return
			}

			out[idx] = PriceFetchResult{SecurityID: s.ID, Ticker: s.Ticker, PriceCAD: priceCAD}
		}(i, sec)
	}
	wg.Wait()
	return out
}

// YahooSymbol converts a ticker and exchange name to a Yahoo Finance symbol.
// Tickers imported from Questrade already carry the exchange suffix (.TO, .V, etc.).
// Yahoo uses hyphens instead of dots for share-class designators (TECK.B.TO → TECK-B.TO),
// so dots in the base ticker are replaced with hyphens before the exchange suffix is appended.
func YahooSymbol(ticker, exchange string) string {
	switch exchange {
	case "TSX":
		base := strings.TrimSuffix(ticker, ".TO")
		return strings.ReplaceAll(base, ".", "-") + ".TO"
	case "TSX-V", "TSXV":
		base := strings.TrimSuffix(ticker, ".V")
		return strings.ReplaceAll(base, ".", "-") + ".V"
	case "NEO", "NEO Exchange":
		base := strings.TrimSuffix(ticker, ".NEO")
		return strings.ReplaceAll(base, ".", "-") + ".NEO"
	default:
		// NYSE, NASDAQ, ARCA, BATS, AMEX, PINX — Yahoo uses hyphens for share classes (BRK-B)
		return strings.ReplaceAll(ticker, ".", "-")
	}
}

type yahooChartResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency           string      `json:"currency"`
				RegularMarketPrice json.Number `json:"regularMarketPrice"`
			} `json:"meta"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

func (f *YahooFetcher) fetchQuote(ctx context.Context, symbol string) (decimal.Decimal, string, error) {
	base, err := url.Parse(f.baseURL)
	if err != nil {
		return decimal.Zero, "", fmt.Errorf("yahoo base URL: %w", err)
	}
	// Set both Path (for semantics) and RawPath (for wire encoding) to avoid
	// double-escaping: url.URL.String() re-encodes Path if RawPath is empty.
	base.Path = "/v8/finance/chart/" + symbol
	base.RawPath = "/v8/finance/chart/" + url.PathEscape(symbol)
	base.RawQuery = "range=1d&interval=1d&includePrePost=false"
	u := base

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), http.NoBody)
	if err != nil {
		return decimal.Zero, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := f.hc.Do(req) //nolint:gosec // URL constructed from fixed base + ticker from our own DB; not user-controlled
	if err != nil {
		return decimal.Zero, "", fmt.Errorf("yahoo %s: %w", symbol, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, "", fmt.Errorf("yahoo %s: HTTP %d", symbol, resp.StatusCode)
	}

	var data yahooChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return decimal.Zero, "", fmt.Errorf("yahoo %s: decode: %w", symbol, err)
	}

	if data.Chart.Error != nil {
		return decimal.Zero, "", fmt.Errorf("yahoo %s: %s: %s", symbol, data.Chart.Error.Code, data.Chart.Error.Description)
	}
	if len(data.Chart.Result) == 0 {
		return decimal.Zero, "", fmt.Errorf("yahoo %s: no result", symbol)
	}

	meta := data.Chart.Result[0].Meta
	price, err := decimal.NewFromString(meta.RegularMarketPrice.String())
	if err != nil || !price.IsPositive() {
		return decimal.Zero, "", fmt.Errorf("yahoo %s: invalid price %q", symbol, meta.RegularMarketPrice.String())
	}

	return price, meta.Currency, nil
}
