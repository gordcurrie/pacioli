package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/fx"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/shopspring/decimal"
)

// stubFXStore is an in-memory fx.Store for testing.
type stubFXStore struct {
	mu    sync.Mutex
	rates map[string]decimal.Decimal // key: "date|from|to"
}

func newStubFXStore() *stubFXStore { return &stubFXStore{rates: make(map[string]decimal.Decimal)} }

func fxKey(date time.Time, from, to string) string {
	return date.UTC().Format(time.DateOnly) + "|" + from + "|" + to
}

func (s *stubFXStore) GetRate(_ context.Context, date time.Time, from, to string) (decimal.Decimal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rates[fxKey(date, from, to)]; ok {
		return r, nil
	}
	return decimal.Zero, errs.ErrNotFound
}

func (s *stubFXStore) StoreRate(_ context.Context, date time.Time, from, to string, rate decimal.Decimal, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rates[fxKey(date, from, to)] = rate
	return nil
}

var _ fx.Store = (*stubFXStore)(nil)

// bocServer builds a test HTTP server that returns a BoC-shaped JSON response.
func bocServer(t *testing.T, obs map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type val struct {
			V string `json:"v"`
		}
		type observation struct {
			D        string `json:"d"`
			FXUSDCAD val    `json:"FXUSDCAD"`
		}
		type body struct {
			Observations []observation `json:"observations"`
		}
		var b body
		for date, rate := range obs {
			b.Observations = append(b.Observations, observation{D: date, FXUSDCAD: val{V: rate}})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(b)
	}))
}

func mustDecimal(s string) decimal.Decimal {
	v, err := decimal.NewFromString(s)
	if err != nil {
		panic(fmt.Sprintf("mustDecimal(%q): %v", s, err))
	}
	return v
}

func bocFetcher(store fx.Store, srv *httptest.Server) *service.BOCFetcher {
	f := service.NewBOCFetcher(store)
	f.BaseURL = srv.URL
	return f
}

// TestUSDCADRate_CacheHit verifies a cached rate is returned without hitting the network.
func TestUSDCADRate_CacheHit(t *testing.T) {
	store := newStubFXStore()
	date := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	want := mustDecimal("1.3500")
	_ = store.StoreRate(context.Background(), date, "USD", "CAD", want, "boc")

	// server that always errors — should never be called
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	got, err := bocFetcher(store, srv).USDCADRate(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got %s want %s", got, want)
	}
}

// TestUSDCADRate_FetchAndCache verifies a business-day rate is fetched and cached.
func TestUSDCADRate_FetchAndCache(t *testing.T) {
	store := newStubFXStore()
	date := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC) // Friday

	srv := bocServer(t, map[string]string{
		"2024-03-15": "1.3612",
		"2024-03-14": "1.3590",
	})
	defer srv.Close()

	got, err := bocFetcher(store, srv).USDCADRate(context.Background(), date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := mustDecimal("1.3612")
	if !got.Equal(want) {
		t.Errorf("got %s want %s", got, want)
	}

	// confirm rate was cached
	cached, err := store.GetRate(context.Background(), date, "USD", "CAD")
	if err != nil {
		t.Fatalf("rate not cached: %v", err)
	}
	if !cached.Equal(want) {
		t.Errorf("cached %s want %s", cached, want)
	}
}

// TestUSDCADRate_WeekendFallback verifies Saturday request returns nearest prior business day.
func TestUSDCADRate_WeekendFallback(t *testing.T) {
	store := newStubFXStore()
	saturday := time.Date(2024, 3, 16, 0, 0, 0, 0, time.UTC) // Saturday
	friday := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)

	srv := bocServer(t, map[string]string{
		"2024-03-15": "1.3612", // Friday — nearest prior business day
	})
	defer srv.Close()

	got, err := bocFetcher(store, srv).USDCADRate(context.Background(), saturday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := mustDecimal("1.3612")
	if !got.Equal(want) {
		t.Errorf("got %s want %s", got, want)
	}

	// Friday rate should be cached, not Saturday
	if _, err := store.GetRate(context.Background(), friday, "USD", "CAD"); err != nil {
		t.Errorf("Friday rate not cached: %v", err)
	}
}

// TestUSDCADRate_NoRate verifies an error is returned when no rate is available.
func TestUSDCADRate_NoRate(t *testing.T) {
	store := newStubFXStore()
	date := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)

	srv := bocServer(t, map[string]string{}) // empty response
	defer srv.Close()

	_, err := bocFetcher(store, srv).USDCADRate(context.Background(), date)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestUSDCADRate_DateNormalization verifies a time with non-zero clock is normalized to midnight UTC.
func TestUSDCADRate_DateNormalization(t *testing.T) {
	store := newStubFXStore()
	// Pre-cache at UTC midnight.
	midnight := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	want := mustDecimal("1.3612")
	_ = store.StoreRate(context.Background(), midnight, "USD", "CAD", want, "boc")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Request with non-zero time and non-UTC zone — should still hit cache.
	est := time.FixedZone("EST", -5*3600)
	noonEST := time.Date(2024, 3, 15, 12, 30, 0, 0, est)

	got, err := bocFetcher(store, srv).USDCADRate(context.Background(), noonEST)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got %s want %s", got, want)
	}
}

// TestUSDCADRate_ServerError verifies a non-200 response from BoC propagates as an error.
func TestUSDCADRate_ServerError(t *testing.T) {
	store := newStubFXStore()
	date := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := bocFetcher(store, srv).USDCADRate(context.Background(), date)
	if err == nil {
		t.Fatal("expected error on non-200 response, got nil")
	}
}
