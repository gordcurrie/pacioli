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
	ngCADTicker  = "DLR"
	ngUSDTicker  = "DLR.U"
	ngExchange   = "TSX"
	ngWindowDays = 3
)

// NGPair is a matched Norbert's Gambit journal pair awaiting linkage.
type NGPair struct {
	GiveLeg    *transaction.Transaction // TypeTransferOut → becomes TypeFXConversion on link
	ReceiveLeg *transaction.Transaction // TypeJournal
}

type NGService struct {
	txStore  transaction.Store
	secStore security.Store
}

func NewNGService(txStore transaction.Store, secStore security.Store) *NGService {
	return &NGService{txStore: txStore, secStore: secStore}
}

// DetectPairs finds unlinked Norbert's Gambit journal pairs for a user.
// Supports both CAD→USD (DLR→DLR.U) and USD→CAD (DLR.U→DLR) directions.
// Matches by quantity and date within ±ngWindowDays.
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

// detectDirectional finds give+receive pairs where give is TypeTransferOut on giveSecID
// and receive is TypeJournal on recvSecID, matched by quantity and date.
func (s *NGService) detectDirectional(ctx context.Context, userID, giveSecID, recvSecID int64) ([]NGPair, error) {
	gives, err := s.txStore.ListUnlinkedBySecurityAndType(ctx, giveSecID, userID, transaction.TypeTransferOut)
	if err != nil {
		return nil, fmt.Errorf("ng detect: give legs (sec %d): %w", giveSecID, err)
	}
	recvs, err := s.txStore.ListUnlinkedBySecurityAndType(ctx, recvSecID, userID, transaction.TypeJournal)
	if err != nil {
		return nil, fmt.Errorf("ng detect: receive legs (sec %d): %w", recvSecID, err)
	}

	used := make(map[int64]bool, len(recvs))
	var pairs []NGPair
	for _, give := range gives {
		if !give.Quantity.IsPositive() {
			continue
		}
		best := matchReceive(give, recvs, used)
		if best == nil {
			continue
		}
		used[best.ID] = true
		pairs = append(pairs, NGPair{GiveLeg: give, ReceiveLeg: best})
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
// within ngWindowDays of the give-leg, or nil if none found.
func matchReceive(give *transaction.Transaction, recvs []*transaction.Transaction, used map[int64]bool) *transaction.Transaction {
	var best *transaction.Transaction
	bestDiff := ngWindowDays + 1
	for _, r := range recvs {
		if used[r.ID] {
			continue
		}
		if !r.Quantity.Equal(give.Quantity) {
			continue
		}
		diff := daysDiff(give.TradeDate, r.TradeDate)
		if diff <= ngWindowDays && diff < bestDiff {
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

// LinkPairs converts each give-leg to TypeFXConversion and sets linked_transaction_id on both legs.
func (s *NGService) LinkPairs(ctx context.Context, pairs []NGPair) (int, error) {
	linked := 0
	for _, p := range pairs {
		if err := s.txStore.LinkNorbertGambitPair(ctx, p.GiveLeg.ID, p.ReceiveLeg.ID); err != nil {
			return linked, fmt.Errorf("ng link pair (give=%d recv=%d): %w", p.GiveLeg.ID, p.ReceiveLeg.ID, err)
		}
		linked++
	}
	return linked, nil
}
