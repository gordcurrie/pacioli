package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

// ngMockTxStore tracks ListUnlinkedBySecurityAndType calls and LinkNorbertGambitPair calls.
type ngMockTxStore struct {
	bySecType map[int64]map[transaction.Type][]*transaction.Transaction
	linked    [][2]int64 // pairs (giveLegID, receiveLegID) passed to LinkNorbertGambitPair
	linkErr   error
}

func (m *ngMockTxStore) Create(_ context.Context, _ *transaction.Transaction) error { return nil }
func (m *ngMockTxStore) GetByID(_ context.Context, _ int64) (*transaction.Transaction, error) {
	return nil, nil
}
func (m *ngMockTxStore) ListByAccount(_ context.Context, _ int64) ([]*transaction.Transaction, error) {
	return nil, nil
}
func (m *ngMockTxStore) ListBySecurityNonRegistered(_ context.Context, _, _ int64) ([]*transaction.Transaction, error) {
	return nil, nil
}
func (m *ngMockTxStore) ListDistinctNonRegisteredSecurityIDsByUser(_ context.Context, _ int64, _, _ time.Time) ([]int64, error) {
	return nil, nil
}
func (m *ngMockTxStore) ListNonRegisteredDisposalsByUser(_ context.Context, _ int64, _, _ time.Time) ([]*transaction.Transaction, error) {
	return nil, nil
}
func (m *ngMockTxStore) ListBySecurityAllAccounts(_ context.Context, _, _ int64) ([]*transaction.Transaction, error) {
	return nil, nil
}
func (m *ngMockTxStore) ListByDateRange(_ context.Context, _ int64, _, _ time.Time) ([]*transaction.Transaction, error) {
	return nil, nil
}
func (m *ngMockTxStore) ListUnlinkedBySecurityAndType(_ context.Context, securityID, _ int64, typ transaction.Type) ([]*transaction.Transaction, error) {
	if m.bySecType == nil {
		return nil, nil
	}
	return m.bySecType[securityID][typ], nil
}
func (m *ngMockTxStore) Delete(_ context.Context, _ int64) error { return nil }
func (m *ngMockTxStore) UpdateFXRate(_ context.Context, _ int64, _ *decimal.Decimal, _, _ decimal.Decimal) error {
	return nil
}
func (m *ngMockTxStore) LinkNorbertGambitPair(_ context.Context, giveID, recvID int64) error {
	if m.linkErr != nil {
		return m.linkErr
	}
	m.linked = append(m.linked, [2]int64{giveID, recvID})
	return nil
}

// ngMockSecStore resolves known (ticker,exchange) pairs and returns ErrNotFound for unknowns.
type ngMockSecStore struct {
	byTickerExchange map[string]*security.Security
}

func (m *ngMockSecStore) Create(_ context.Context, _ *security.Security) error { return nil }
func (m *ngMockSecStore) GetByID(_ context.Context, _ int64) (*security.Security, error) {
	return nil, nil
}
func (m *ngMockSecStore) GetByTickerExchange(_ context.Context, ticker, exchange string) (*security.Security, error) {
	if s, ok := m.byTickerExchange[ticker+"|"+exchange]; ok {
		return s, nil
	}
	return nil, errs.ErrNotFound
}
func (m *ngMockSecStore) Search(_ context.Context, _ string) ([]*security.Security, error) {
	return nil, nil
}
func (m *ngMockSecStore) ListAll(_ context.Context) ([]*security.Security, error) { return nil, nil }
func (m *ngMockSecStore) Update(_ context.Context, _ *security.Security) error    { return nil }
func (m *ngMockSecStore) Delete(_ context.Context, _ int64) error                 { return nil }

func ngDate(s string) time.Time {
	t, _ := time.Parse(time.DateOnly, s)
	return t
}

func ngQty(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

func TestNGService_DetectPairs_MatchesByQuantityAndDate(t *testing.T) {
	dlrSec := &security.Security{ID: 1, Ticker: "DLR", Exchange: "TSX"}
	dlruSec := &security.Security{ID: 2, Ticker: "DLR.U", Exchange: "TSX"}

	give := &transaction.Transaction{ID: 10, SecurityID: 1, Type: transaction.TypeTransferOut,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("200")}
	recv := &transaction.Transaction{ID: 20, SecurityID: 2, Type: transaction.TypeJournal,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("200")}

	txStore := &ngMockTxStore{
		bySecType: map[int64]map[transaction.Type][]*transaction.Transaction{
			1: {transaction.TypeTransferOut: []*transaction.Transaction{give}},
			2: {transaction.TypeJournal: []*transaction.Transaction{recv}},
		},
	}
	secStore := &ngMockSecStore{byTickerExchange: map[string]*security.Security{
		"DLR|TSX":   dlrSec,
		"DLR.U|TSX": dlruSec,
	}}

	svc := service.NewNGService(txStore, secStore)
	pairs, err := svc.DetectPairs(context.Background(), 1)
	if err != nil {
		t.Fatalf("DetectPairs: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].GiveLeg.ID != 10 || pairs[0].ReceiveLeg.ID != 20 {
		t.Errorf("wrong pair: give=%d recv=%d", pairs[0].GiveLeg.ID, pairs[0].ReceiveLeg.ID)
	}
}

func TestNGService_DetectPairs_NoMatchWhenQuantityDiffers(t *testing.T) {
	dlrSec := &security.Security{ID: 1, Ticker: "DLR", Exchange: "TSX"}
	dlruSec := &security.Security{ID: 2, Ticker: "DLR.U", Exchange: "TSX"}

	give := &transaction.Transaction{ID: 10, SecurityID: 1, Type: transaction.TypeTransferOut,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("200")}
	recv := &transaction.Transaction{ID: 20, SecurityID: 2, Type: transaction.TypeJournal,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("100")} // different qty

	txStore := &ngMockTxStore{
		bySecType: map[int64]map[transaction.Type][]*transaction.Transaction{
			1: {transaction.TypeTransferOut: []*transaction.Transaction{give}},
			2: {transaction.TypeJournal: []*transaction.Transaction{recv}},
		},
	}
	secStore := &ngMockSecStore{byTickerExchange: map[string]*security.Security{
		"DLR|TSX":   dlrSec,
		"DLR.U|TSX": dlruSec,
	}}

	svc := service.NewNGService(txStore, secStore)
	pairs, err := svc.DetectPairs(context.Background(), 1)
	if err != nil {
		t.Fatalf("DetectPairs: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs when quantity differs, got %d", len(pairs))
	}
}

func TestNGService_DetectPairs_NoMatchWhenTooFarApart(t *testing.T) {
	dlrSec := &security.Security{ID: 1, Ticker: "DLR", Exchange: "TSX"}
	dlruSec := &security.Security{ID: 2, Ticker: "DLR.U", Exchange: "TSX"}

	give := &transaction.Transaction{ID: 10, SecurityID: 1, Type: transaction.TypeTransferOut,
		TradeDate: ngDate("2024-06-01"), Quantity: ngQty("100")}
	recv := &transaction.Transaction{ID: 20, SecurityID: 2, Type: transaction.TypeJournal,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("100")} // 14 days apart

	txStore := &ngMockTxStore{
		bySecType: map[int64]map[transaction.Type][]*transaction.Transaction{
			1: {transaction.TypeTransferOut: []*transaction.Transaction{give}},
			2: {transaction.TypeJournal: []*transaction.Transaction{recv}},
		},
	}
	secStore := &ngMockSecStore{byTickerExchange: map[string]*security.Security{
		"DLR|TSX":   dlrSec,
		"DLR.U|TSX": dlruSec,
	}}

	svc := service.NewNGService(txStore, secStore)
	pairs, err := svc.DetectPairs(context.Background(), 1)
	if err != nil {
		t.Fatalf("DetectPairs: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs when date gap > 3 days, got %d", len(pairs))
	}
}

func TestNGService_DetectPairs_MissingSecurityReturnsEmpty(t *testing.T) {
	// DLR.U not in DB → no pairs possible
	dlrSec := &security.Security{ID: 1, Ticker: "DLR", Exchange: "TSX"}
	txStore := &ngMockTxStore{}
	secStore := &ngMockSecStore{byTickerExchange: map[string]*security.Security{
		"DLR|TSX": dlrSec,
		// DLR.U missing
	}}

	svc := service.NewNGService(txStore, secStore)
	pairs, err := svc.DetectPairs(context.Background(), 1)
	if err != nil {
		t.Fatalf("DetectPairs with missing security: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs, got %d", len(pairs))
	}
}

func TestNGService_LinkPairs_CallsStore(t *testing.T) {
	dlrSec := &security.Security{ID: 1, Ticker: "DLR", Exchange: "TSX"}
	dlruSec := &security.Security{ID: 2, Ticker: "DLR.U", Exchange: "TSX"}

	give := &transaction.Transaction{ID: 10, SecurityID: 1, Type: transaction.TypeTransferOut,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("100")}
	recv := &transaction.Transaction{ID: 20, SecurityID: 2, Type: transaction.TypeJournal,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("100")}

	txStore := &ngMockTxStore{
		bySecType: map[int64]map[transaction.Type][]*transaction.Transaction{
			1: {transaction.TypeTransferOut: []*transaction.Transaction{give}},
			2: {transaction.TypeJournal: []*transaction.Transaction{recv}},
		},
	}
	secStore := &ngMockSecStore{byTickerExchange: map[string]*security.Security{
		"DLR|TSX":   dlrSec,
		"DLR.U|TSX": dlruSec,
	}}

	svc := service.NewNGService(txStore, secStore)
	pairs, _ := svc.DetectPairs(context.Background(), 1)
	n, err := svc.LinkPairs(context.Background(), pairs)
	if err != nil {
		t.Fatalf("LinkPairs: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 linked, got %d", n)
	}
	if len(txStore.linked) != 1 {
		t.Fatalf("expected 1 store call, got %d", len(txStore.linked))
	}
	if txStore.linked[0][0] != 10 || txStore.linked[0][1] != 20 {
		t.Errorf("wrong IDs linked: %v", txStore.linked[0])
	}
}

func TestNGService_DetectPairs_SkipsZeroQuantity(t *testing.T) {
	dlrSec := &security.Security{ID: 1, Ticker: "DLR", Exchange: "TSX"}
	dlruSec := &security.Security{ID: 2, Ticker: "DLR.U", Exchange: "TSX"}

	zeroGive := &transaction.Transaction{ID: 10, SecurityID: 1, Type: transaction.TypeTransferOut,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("0")}
	zeroRecv := &transaction.Transaction{ID: 20, SecurityID: 2, Type: transaction.TypeJournal,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("0")}

	txStore := &ngMockTxStore{
		bySecType: map[int64]map[transaction.Type][]*transaction.Transaction{
			1: {transaction.TypeTransferOut: []*transaction.Transaction{zeroGive}},
			2: {transaction.TypeJournal: []*transaction.Transaction{zeroRecv}},
		},
	}
	secStore := &ngMockSecStore{byTickerExchange: map[string]*security.Security{
		"DLR|TSX":   dlrSec,
		"DLR.U|TSX": dlruSec,
	}}

	svc := service.NewNGService(txStore, secStore)
	pairs, err := svc.DetectPairs(context.Background(), 1)
	if err != nil {
		t.Fatalf("DetectPairs: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs for zero-quantity transactions, got %d", len(pairs))
	}
}

func TestNGService_DetectPairs_USDToCAD(t *testing.T) {
	// Reverse direction: TransferOut on DLR.U + Journal on DLR
	dlrSec := &security.Security{ID: 1, Ticker: "DLR", Exchange: "TSX"}
	dlruSec := &security.Security{ID: 2, Ticker: "DLR.U", Exchange: "TSX"}

	give := &transaction.Transaction{ID: 30, SecurityID: 2, Type: transaction.TypeTransferOut,
		TradeDate: ngDate("2024-09-10"), Quantity: ngQty("500")}
	recv := &transaction.Transaction{ID: 40, SecurityID: 1, Type: transaction.TypeJournal,
		TradeDate: ngDate("2024-09-10"), Quantity: ngQty("500")}

	txStore := &ngMockTxStore{
		bySecType: map[int64]map[transaction.Type][]*transaction.Transaction{
			2: {transaction.TypeTransferOut: []*transaction.Transaction{give}},
			1: {transaction.TypeJournal: []*transaction.Transaction{recv}},
		},
	}
	secStore := &ngMockSecStore{byTickerExchange: map[string]*security.Security{
		"DLR|TSX":   dlrSec,
		"DLR.U|TSX": dlruSec,
	}}

	svc := service.NewNGService(txStore, secStore)
	pairs, err := svc.DetectPairs(context.Background(), 1)
	if err != nil {
		t.Fatalf("DetectPairs: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected 1 USD→CAD pair, got %d", len(pairs))
	}
	if pairs[0].GiveLeg.ID != 30 || pairs[0].ReceiveLeg.ID != 40 {
		t.Errorf("wrong pair: give=%d recv=%d", pairs[0].GiveLeg.ID, pairs[0].ReceiveLeg.ID)
	}
}

func TestNGService_DetectPairs_QuestradeSuffixedTickers(t *testing.T) {
	// Questrade-synced securities use "DLR.TO" / "DLR.U.TO" as tickers.
	dlrSec := &security.Security{ID: 1, Ticker: "DLR.TO", Exchange: "TSX"}
	dlruSec := &security.Security{ID: 2, Ticker: "DLR.U.TO", Exchange: "TSX"}

	give := &transaction.Transaction{ID: 10, SecurityID: 1, Type: transaction.TypeTransferOut,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("200")}
	recv := &transaction.Transaction{ID: 20, SecurityID: 2, Type: transaction.TypeJournal,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("200")}

	txStore := &ngMockTxStore{
		bySecType: map[int64]map[transaction.Type][]*transaction.Transaction{
			1: {transaction.TypeTransferOut: []*transaction.Transaction{give}},
			2: {transaction.TypeJournal: []*transaction.Transaction{recv}},
		},
	}
	// Store indexed by the suffixed tickers that Questrade sync produces.
	secStore := &ngMockSecStore{byTickerExchange: map[string]*security.Security{
		"DLR.TO|TSX":   dlrSec,
		"DLR.U.TO|TSX": dlruSec,
	}}

	svc := service.NewNGService(txStore, secStore)
	pairs, err := svc.DetectPairs(context.Background(), 1)
	if err != nil {
		t.Fatalf("DetectPairs with suffixed tickers: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair with suffixed tickers, got %d", len(pairs))
	}
	if pairs[0].GiveLeg.ID != 10 || pairs[0].ReceiveLeg.ID != 20 {
		t.Errorf("wrong pair: give=%d recv=%d", pairs[0].GiveLeg.ID, pairs[0].ReceiveLeg.ID)
	}
}

func TestNGService_DetectPairs_PicksClosestReceiveLeg(t *testing.T) {
	// Two receive legs matching same qty: one same day, one 2 days later — pick same day.
	dlrSec := &security.Security{ID: 1, Ticker: "DLR", Exchange: "TSX"}
	dlruSec := &security.Security{ID: 2, Ticker: "DLR.U", Exchange: "TSX"}

	give := &transaction.Transaction{ID: 10, SecurityID: 1, Type: transaction.TypeTransferOut,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("100")}
	recvClose := &transaction.Transaction{ID: 20, SecurityID: 2, Type: transaction.TypeJournal,
		TradeDate: ngDate("2024-06-15"), Quantity: ngQty("100")}
	recvFar := &transaction.Transaction{ID: 21, SecurityID: 2, Type: transaction.TypeJournal,
		TradeDate: ngDate("2024-06-17"), Quantity: ngQty("100")}

	txStore := &ngMockTxStore{
		bySecType: map[int64]map[transaction.Type][]*transaction.Transaction{
			1: {transaction.TypeTransferOut: []*transaction.Transaction{give}},
			2: {transaction.TypeJournal: []*transaction.Transaction{recvClose, recvFar}},
		},
	}
	secStore := &ngMockSecStore{byTickerExchange: map[string]*security.Security{
		"DLR|TSX":   dlrSec,
		"DLR.U|TSX": dlruSec,
	}}

	svc := service.NewNGService(txStore, secStore)
	pairs, err := svc.DetectPairs(context.Background(), 1)
	if err != nil {
		t.Fatalf("DetectPairs: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected 1 pair, got %d", len(pairs))
	}
	if pairs[0].ReceiveLeg.ID != 20 {
		t.Errorf("expected closest receive leg (ID=20), got ID=%d", pairs[0].ReceiveLeg.ID)
	}
}
