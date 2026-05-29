package service

import (
	"context"
	"fmt"

	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type ACBResult struct {
	SecurityID  int64
	Shares      decimal.Decimal
	TotalACB    decimal.Decimal
	ACBPerShare decimal.Decimal
}

type ACBService struct {
	txStore transaction.Store
}

func NewACBService(txStore transaction.Store) *ACBService {
	return &ACBService{txStore: txStore}
}

func (s *ACBService) Calculate(ctx context.Context, securityID, userID int64) (*ACBResult, error) {
	txs, err := s.txStore.ListBySecurityNonRegistered(ctx, securityID, userID)
	if err != nil {
		return nil, fmt.Errorf("acb calculate: %w", err)
	}
	return CalculateACB(securityID, txs), nil
}

// CalculateACB computes ACB from an ordered (trade_date asc) transaction slice.
// Exported so it can be tested without a store.
func CalculateACB(securityID int64, txs []*transaction.Transaction) *ACBResult {
	r := &ACBResult{SecurityID: securityID}
	for _, tx := range txs {
		switch tx.Type {
		case transaction.TypeBuy, transaction.TypeTransferIn, transaction.TypeJournal:
			cost := tx.Quantity.Mul(tx.PriceCAD).Add(tx.CommissionCAD)
			r.TotalACB = r.TotalACB.Add(cost)
			r.Shares = r.Shares.Add(tx.Quantity)
		case transaction.TypeSell, transaction.TypeTransferOut:
			if r.Shares.IsPositive() {
				acbPerShare := r.TotalACB.Div(r.Shares)
				r.TotalACB = r.TotalACB.Sub(acbPerShare.Mul(tx.Quantity))
			}
			r.Shares = r.Shares.Sub(tx.Quantity)
		case transaction.TypeROCAdjustment:
			// Quantity = shares held at T3 record date; PriceCAD = ROC per unit
			r.TotalACB = r.TotalACB.Sub(tx.Quantity.Mul(tx.PriceCAD))
		case transaction.TypeDividend, transaction.TypeFXConversion:
			// no ACB impact
		}
	}
	if r.Shares.IsPositive() {
		r.ACBPerShare = r.TotalACB.Div(r.Shares)
	}
	return r
}
