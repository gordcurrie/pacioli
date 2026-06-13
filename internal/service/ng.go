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

// giveTicker / receiveTicker / ngExchange identify the Norbert's Gambit security pair.
// DLR (CAD) is journalled to DLR.U (USD) on the TSX.
const (
	ngGiveTicker    = "DLR"
	ngReceiveTicker = "DLR.U"
	ngExchange      = "TSX"
	ngWindowDays    = 3
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
// It looks for TypeTransferOut on DLR matched by quantity and date (±ngWindowDays)
// to TypeJournal on DLR.U that are not yet linked.
func (s *NGService) DetectPairs(ctx context.Context, userID int64) ([]NGPair, error) {
	giveSec, err := s.secStore.GetByTickerExchange(ctx, ngGiveTicker, ngExchange)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("ng detect: give security: %w", err)
	}
	recvSec, err := s.secStore.GetByTickerExchange(ctx, ngReceiveTicker, ngExchange)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("ng detect: receive security: %w", err)
	}

	gives, err := s.txStore.ListUnlinkedBySecurityAndType(ctx, giveSec.ID, userID, transaction.TypeTransferOut)
	if err != nil {
		return nil, fmt.Errorf("ng detect: give legs: %w", err)
	}
	recvs, err := s.txStore.ListUnlinkedBySecurityAndType(ctx, recvSec.ID, userID, transaction.TypeJournal)
	if err != nil {
		return nil, fmt.Errorf("ng detect: receive legs: %w", err)
	}

	used := make(map[int64]bool, len(recvs))
	var pairs []NGPair
	for _, give := range gives {
		best := matchReceive(give, recvs, used)
		if best == nil {
			continue
		}
		used[best.ID] = true
		pairs = append(pairs, NGPair{GiveLeg: give, ReceiveLeg: best})
	}
	return pairs, nil
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
