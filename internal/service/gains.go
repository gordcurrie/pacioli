package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type GainsLine struct {
	Security          *security.Security
	TradeDate         time.Time
	Shares            decimal.Decimal
	ProceedsCAD       decimal.Decimal
	ACBAtSell         decimal.Decimal
	GainLoss          decimal.Decimal
	IsSuperficialLoss bool
}

type GainsReport struct {
	Year                 int
	Lines                []GainsLine
	TotalGains           decimal.Decimal
	TotalLosses          decimal.Decimal
	NetGain              decimal.Decimal
	SuperficialLossTotal decimal.Decimal
}

type GainsService struct {
	txStore  transaction.Store
	secStore security.Store
}

func NewGainsService(txStore transaction.Store, secStore security.Store) *GainsService {
	return &GainsService{txStore: txStore, secStore: secStore}
}

func (s *GainsService) Calculate(ctx context.Context, userID int64, year int) (*GainsReport, error) {
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)

	sells, err := s.txStore.ListNonRegisteredSellsByUser(ctx, userID, from, to)
	if err != nil {
		return nil, fmt.Errorf("gains calculate: %w", err)
	}

	// collect distinct security IDs that had sells this year
	seen := make(map[int64]struct{})
	for _, tx := range sells {
		seen[tx.SecurityID] = struct{}{}
	}

	report := &GainsReport{Year: year}

	for secID := range seen {
		sec, err := s.secStore.GetByID(ctx, secID)
		if err != nil {
			return nil, fmt.Errorf("gains calculate: get security %d: %w", secID, err)
		}

		txs, err := s.txStore.ListBySecurityNonRegistered(ctx, secID, userID)
		if err != nil {
			return nil, fmt.Errorf("gains calculate: list txs for security %d: %w", secID, err)
		}

		_, history := CalculateACBWithHistory(secID, txs)

		for _, row := range history {
			if row.Tx.Type != transaction.TypeSell || row.Tx.TradeDate.Year() != year {
				continue
			}

			proceeds := row.Tx.Quantity.Mul(row.Tx.PriceCAD).Sub(row.Tx.CommissionCAD)
			acbAtSell := row.PreTxACBPerShare.Mul(row.Tx.Quantity)
			gainLoss := proceeds.Sub(acbAtSell)

			line := GainsLine{
				Security:    sec,
				TradeDate:   row.Tx.TradeDate,
				Shares:      row.Tx.Quantity,
				ProceedsCAD: proceeds,
				ACBAtSell:   acbAtSell,
				GainLoss:    gainLoss,
			}

			if gainLoss.IsNegative() {
				superficial, err := s.isSuperficialLoss(ctx, secID, userID, row.Tx.TradeDate)
				if err != nil {
					return nil, fmt.Errorf("gains calculate: superficial loss check: %w", err)
				}
				line.IsSuperficialLoss = superficial
			}

			if gainLoss.IsPositive() {
				report.TotalGains = report.TotalGains.Add(gainLoss)
			} else if gainLoss.IsNegative() {
				report.TotalLosses = report.TotalLosses.Add(gainLoss.Abs())
			}
			if line.IsSuperficialLoss {
				report.SuperficialLossTotal = report.SuperficialLossTotal.Add(gainLoss.Abs())
			}

			report.Lines = append(report.Lines, line)
		}
	}

	report.NetGain = report.TotalGains.Sub(report.TotalLosses)
	return report, nil
}

func (s *GainsService) isSuperficialLoss(ctx context.Context, securityID, userID int64, sellDate time.Time) (bool, error) {
	allTxs, err := s.txStore.ListBySecurityAllAccounts(ctx, securityID, userID)
	if err != nil {
		return false, err
	}
	windowStart := sellDate.AddDate(0, 0, -30)
	windowEnd := sellDate.AddDate(0, 0, 30)
	for _, tx := range allTxs {
		if tx.Type != transaction.TypeBuy && tx.Type != transaction.TypeTransferIn {
			continue
		}
		if !tx.TradeDate.Before(windowStart) && !tx.TradeDate.After(windowEnd) {
			return true, nil
		}
	}
	return false, nil
}
