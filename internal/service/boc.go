package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/fx"
	"github.com/shopspring/decimal"
)

const bocURL = "https://www.bankofcanada.ca/valet/observations/FXUSDCAD/json"

// BOCFetcher fetches USD/CAD noon rates from the Bank of Canada and caches
// them in the fx_rates table. It is the CRA-accepted authoritative source.
type BOCFetcher struct {
	store   fx.Store
	hc      *http.Client
	BaseURL string // overridable in tests; defaults to bocURL
}

func NewBOCFetcher(store fx.Store) *BOCFetcher {
	return &BOCFetcher{
		store:   store,
		hc:      &http.Client{Timeout: 15 * time.Second},
		BaseURL: bocURL,
	}
}

// USDCADRate returns the BoC noon USD/CAD rate for the given date. If the date
// is a weekend or holiday, the nearest prior business day rate is used.
func (f *BOCFetcher) USDCADRate(ctx context.Context, date time.Time) (decimal.Decimal, error) {
	// Normalize to UTC midnight so map keys match fetchBOC's parsed dates.
	date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)

	// Check cache for the date and up to 5 prior business days.
	for i := 0; i <= 5; i++ {
		d := date.AddDate(0, 0, -i)
		rate, err := f.store.GetRate(ctx, d, "USD", "CAD")
		if err == nil {
			return rate, nil
		}
		if !errors.Is(err, errs.ErrNotFound) {
			return decimal.Zero, err
		}
	}

	// Not cached: fetch an 11-day window (date-10 through date+1) from BoC and populate the cache.
	start := date.AddDate(0, 0, -10)
	end := date.AddDate(0, 0, 1)
	fetched, err := f.fetchBOC(ctx, start, end)
	if err != nil {
		return decimal.Zero, err
	}
	for d, r := range fetched {
		if err := f.store.StoreRate(ctx, d, "USD", "CAD", r, "boc"); err != nil {
			return decimal.Zero, fmt.Errorf("boc: cache rate %s: %w", d.Format(time.DateOnly), err)
		}
	}

	// Walk back to find nearest available rate.
	for i := 0; i <= 5; i++ {
		d := date.AddDate(0, 0, -i)
		if r, ok := fetched[d]; ok {
			return r, nil
		}
	}
	return decimal.Zero, fmt.Errorf("boc: no USD/CAD rate for %s (checked 5 prior days)", date.Format(time.DateOnly))
}

type bocObservation struct {
	D        string `json:"d"`
	FXUSDCAD struct {
		V string `json:"v"`
	} `json:"FXUSDCAD"`
}

type bocResponse struct {
	Observations []bocObservation `json:"observations"`
}

func (f *BOCFetcher) fetchBOC(ctx context.Context, start, end time.Time) (map[time.Time]decimal.Decimal, error) {
	rawURL := fmt.Sprintf("%s?start_date=%s&end_date=%s",
		f.BaseURL, start.Format(time.DateOnly), end.Format(time.DateOnly))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody) // #nosec G107 G704 -- BaseURL defaults to bocURL constant; only overridden in tests; date params from time.Time.Format
	if err != nil {
		return nil, fmt.Errorf("boc fetch: %w", err)
	}
	resp, err := f.hc.Do(req) // #nosec G704 -- BaseURL is code-controlled (defaults to bocURL constant; only overridden in tests)
	if err != nil {
		return nil, fmt.Errorf("boc fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("boc fetch: status %d", resp.StatusCode)
	}

	var body bocResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("boc fetch: decode: %w", err)
	}

	rates := make(map[time.Time]decimal.Decimal, len(body.Observations))
	for _, obs := range body.Observations {
		d, err := time.Parse(time.DateOnly, obs.D)
		if err != nil || obs.FXUSDCAD.V == "" {
			continue
		}
		rate, err := decimal.NewFromString(obs.FXUSDCAD.V)
		if err != nil {
			continue
		}
		rates[d] = rate
	}
	return rates, nil
}
