package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/transaction"
)

// ngCADTicker / ngUSDTicker / ngExchange identify the Norbert's Gambit security pair.
// DLR (CAD) is journalled to DLR.U (USD) on the TSX.
// Questrade-synced securities append ".TO" (e.g. "DLR.TO"); lookupNGSecurity tries both forms.
const (
	ngCADTicker      = "DLR"
	ngUSDTicker      = "DLR.U"
	ngExchange       = "TSX"
	ngWindowDays     = 3  // journal path: TypeTransferOut → TypeJournal, same-day or ±3d
	ngDirectWindowDays = 10 // direct path: TypeBuy → TypeSell, broker didn't report journal (Cash accounts)
)

// NGPair is a matched Norbert's Gambit journal pair awaiting linkage.
type NGPair struct {
	GiveLeg    *transaction.Transaction // TypeTransferOut (journal) or TypeBuy (direct)
	ReceiveLeg *transaction.Transaction // TypeJournal (journal) or TypeSell (direct)
	IsDirect   bool                     // true when broker did not report intermediate journal transactions
}

// NGService detects and links unlinked Norbert's Gambit transaction pairs.
type NGService struct {
	txStore  transaction.Store
	secStore security.Store
}

// NewNGService constructs an NGService backed by the given stores.
func NewNGService(txStore transaction.Store, secStore security.Store) *NGService {
	return &NGService{txStore: txStore, secStore: secStore}
}

// DetectPairs finds unlinked Norbert's Gambit journal pairs for a user.
// Supports both CAD→USD (DLR→DLR.U) and USD→CAD (DLR.U→DLR) directions.
// Matches by quantity and date within ±ngWindowDays (journal) or ±ngDirectWindowDays (direct).
func (s *NGService) DetectPairs(ctx context.Context, userID int64) ([]NGPair, error) {
	cadSec, err := s.lookupNGSecurity(ctx, ngCADTicker, ngExchange)
	if err != nil {
		return nil, fmt.Errorf("ng detect: CAD security: %w", err)
	}
	usdSec, err := s.lookupNGSecurity(ctx, ngUSDTicker, ngExchange)
	if err != nil {
		return nil, fmt.Errorf("ng detect: USD security: %w", err)
	}
	if cadSec == nil || usdSec == nil {
		return nil, nil
	}

	cadToUSD, err := s.detectDirectional(ctx, userID, cadSec.ID, usdSec.ID)
	if err != nil {
		return nil, err
	}
	usdToCAD, err := s.detectDirectional(ctx, userID, usdSec.ID, cadSec.ID)
	if err != nil {
		return nil, err
	}
	return append(cadToUSD, usdToCAD...), nil
}

// detectDirectional finds give+receive pairs in both the journal path and the direct path.
//
// Journal path: give=TypeTransferOut on giveSecID, receive=TypeJournal on recvSecID.
// Used when the broker (e.g. Questrade margin/TFSA) reports the intermediate journal step.
//
// Direct path: give=TypeBuy on giveSecID, receive=TypeSell on recvSecID.
// Used when the broker (e.g. Questrade Cash) does NOT report journal transactions; only the
// original buy and sell appear. TypeBuy candidates already covered by the journal path are
// skipped to avoid double-detection on accounts that report both.
func (s *NGService) detectDirectional(ctx context.Context, userID, giveSecID, recvSecID int64) ([]NGPair, error) {
	// --- Journal path ---
	gives, err := s.txStore.ListUnlinkedBySecurityAndType(ctx, giveSecID, userID, transaction.TypeTransferOut)
	if err != nil {
		return nil, fmt.Errorf("ng detect: give legs (sec %d): %w", giveSecID, err)
	}
	recvs, err := s.txStore.ListUnlinkedBySecurityAndType(ctx, recvSecID, userID, transaction.TypeJournal)
	if err != nil {
		return nil, fmt.Errorf("ng detect: receive legs (sec %d): %w", recvSecID, err)
	}

	used := make(map[int64]bool, len(recvs))
	// xferCovered tracks qty|date covered by a TypeTransferOut give so the direct path
	// does not double-detect TypeBuy transactions on the same day.
	xferCovered := make(map[string]bool)
	var pairs []NGPair
	for _, give := range gives {
		if !give.Quantity.IsPositive() {
			continue
		}
		best := matchReceive(give, recvs, used, ngWindowDays)
		if best == nil {
			continue
		}
		used[best.ID] = true
		xferCovered[give.Quantity.String()+"|"+give.TradeDate.Format(time.DateOnly)] = true
		pairs = append(pairs, NGPair{GiveLeg: give, ReceiveLeg: best})
	}

	// --- Direct path ---
	buyGives, err := s.txStore.ListUnlinkedBySecurityAndType(ctx, giveSecID, userID, transaction.TypeBuy)
	if err != nil {
		return nil, fmt.Errorf("ng detect: direct give legs (sec %d): %w", giveSecID, err)
	}
	sellRecvs, err := s.txStore.ListUnlinkedBySecurityAndType(ctx, recvSecID, userID, transaction.TypeSell)
	if err != nil {
		return nil, fmt.Errorf("ng detect: direct receive legs (sec %d): %w", recvSecID, err)
	}

	usedSell := make(map[int64]bool, len(sellRecvs))
	for _, give := range buyGives {
		if !give.Quantity.IsPositive() {
			continue
		}
		if xferCovered[give.Quantity.String()+"|"+give.TradeDate.Format(time.DateOnly)] {
			continue
		}
		best := matchReceive(give, sellRecvs, usedSell, ngDirectWindowDays)
		if best == nil {
			continue
		}
		usedSell[best.ID] = true
		pairs = append(pairs, NGPair{GiveLeg: give, ReceiveLeg: best, IsDirect: true})
	}

	return pairs, nil
}

// lookupNGSecurity finds the security for ticker on exchange.
// Tries the plain ticker first (manually-entered securities), then the ".TO"-suffixed form
// (Questrade-synced securities which store the full exchange symbol, e.g. "DLR.TO").
// Returns nil, nil when the security is not found in any form.
func (s *NGService) lookupNGSecurity(ctx context.Context, ticker, exchange string) (*security.Security, error) {
	sec, err := s.secStore.GetByTickerExchange(ctx, ticker, exchange)
	if err == nil {
		return sec, nil
	}
	if !errors.Is(err, errs.ErrNotFound) {
		return nil, err
	}
	sec, err = s.secStore.GetByTickerExchange(ctx, ticker+".TO", exchange)
	if err == nil {
		return sec, nil
	}
	if errors.Is(err, errs.ErrNotFound) {
		return nil, nil
	}
	return nil, err
}

// matchReceive returns the closest-date unlinked receive-leg with the same quantity
// and account within windowDays of the give-leg, or nil if none found.
func matchReceive(give *transaction.Transaction, recvs []*transaction.Transaction, used map[int64]bool, windowDays int) *transaction.Transaction {
	var best *transaction.Transaction
	bestDiff := windowDays + 1
	for _, r := range recvs {
		if used[r.ID] {
			continue
		}
		if r.AccountID != give.AccountID {
			continue
		}
		if !r.Quantity.Equal(give.Quantity) {
			continue
		}
		diff := daysDiff(give.TradeDate, r.TradeDate)
		if diff <= windowDays && diff < bestDiff {
			best = r
			bestDiff = diff
		}
	}
	return best
}

func daysDiff(a, b time.Time) int {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return int(d.Hours() / 24)
}

// LinkPairs links each detected pair. Journal pairs convert the give-leg to TypeFXConversion
// in place; direct pairs create synthetic TypeFXConversion + TypeJournal records.
func (s *NGService) LinkPairs(ctx context.Context, pairs []NGPair) (int, error) {
	linked := 0
	for _, p := range pairs {
		var err error
		if p.IsDirect {
			err = s.txStore.LinkNorbertGambitPairDirect(ctx, p.GiveLeg, p.ReceiveLeg)
		} else {
			err = s.txStore.LinkNorbertGambitPair(ctx, p.GiveLeg.ID, p.ReceiveLeg.ID)
		}
		if err != nil {
			return linked, fmt.Errorf("ng link pair (give=%d recv=%d): %w", p.GiveLeg.ID, p.ReceiveLeg.ID, err)
		}
		linked++
	}
	return linked, nil
}
