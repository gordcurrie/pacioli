package service

import (
	"context"
	"slices"
	"strings"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

// PortfolioPosition is one open position in the portfolio dashboard.
type PortfolioPosition struct {
	Security          *security.Security
	TotalShares       decimal.Decimal // across all accounts (reg + non-reg)
	NonRegShares      decimal.Decimal // non-registered only (ACB-tracked)
	NonRegACBTotal    decimal.Decimal
	NonRegACBPerShare decimal.Decimal
	HasPrice          bool
	CurrentValueCAD   decimal.Decimal // TotalShares × last_price_cad; zero when no price
	UnrealizedGainCAD decimal.Decimal // non-reg value - non-reg ACB; zero when no price or no non-reg shares
}

// PortfolioSummary aggregates totals across all positions.
type PortfolioSummary struct {
	TotalValueCAD      decimal.Decimal
	TotalNonRegACB     decimal.Decimal
	TotalUnrealizedCAD decimal.Decimal
	ValueCADDenom      decimal.Decimal // value of CAD-denominated positions
	ValueUSDDenom      decimal.Decimal // CAD-equivalent value of USD-denominated positions
}

// PortfolioService builds the portfolio dashboard view.
type PortfolioService struct {
	txStore  transaction.Store
	secStore security.Store
	acbSvc   *ACBService
}

// NewPortfolioService constructs a PortfolioService.
func NewPortfolioService(txStore transaction.Store, secStore security.Store, acbSvc *ACBService) *PortfolioService {
	return &PortfolioService{txStore: txStore, secStore: secStore, acbSvc: acbSvc}
}

// Build returns open positions and summary totals for the given user.
func (s *PortfolioService) Build(ctx context.Context, userID int64) ([]PortfolioPosition, PortfolioSummary, error) {
	secIDs, err := s.txStore.DistinctAllSecurityIDsByUser(ctx, userID)
	if err != nil {
		return nil, PortfolioSummary{}, err
	}

	allSecs, err := s.secStore.GetByIDs(ctx, secIDs)
	if err != nil {
		return nil, PortfolioSummary{}, err
	}
	secMap := make(map[int64]*security.Security, len(allSecs))
	for _, sec := range allSecs {
		secMap[sec.ID] = sec
	}

	var positions []PortfolioPosition
	var summary PortfolioSummary

	for _, secID := range secIDs {
		sec, ok := secMap[secID]
		if !ok {
			continue
		}

		allTxs, err := s.txStore.ListBySecurityAllAccounts(ctx, secID, userID)
		if err != nil {
			return nil, PortfolioSummary{}, err
		}
		totalShares := netShares(allTxs)
		if !totalShares.IsPositive() {
			continue
		}

		nonRegTxs, err := s.txStore.ListBySecurityNonRegistered(ctx, secID, userID)
		if err != nil {
			return nil, PortfolioSummary{}, err
		}
		acb := s.acbSvc.calculateFromTxs(secID, nonRegTxs, allTxs)

		pos := PortfolioPosition{
			Security:          sec,
			TotalShares:       totalShares,
			NonRegShares:      acb.Shares,
			NonRegACBTotal:    acb.TotalACB,
			NonRegACBPerShare: acb.ACBPerShare,
		}

		if sec.LastPriceCAD != nil {
			pos.HasPrice = true
			pos.CurrentValueCAD = totalShares.Mul(*sec.LastPriceCAD)
			if acb.Shares.IsPositive() {
				pos.UnrealizedGainCAD = acb.Shares.Mul(*sec.LastPriceCAD).Sub(acb.TotalACB)
			}
			summary.TotalValueCAD = summary.TotalValueCAD.Add(pos.CurrentValueCAD)
			if sec.Currency == "USD" {
				summary.ValueUSDDenom = summary.ValueUSDDenom.Add(pos.CurrentValueCAD)
			} else {
				summary.ValueCADDenom = summary.ValueCADDenom.Add(pos.CurrentValueCAD)
			}
		}
		if acb.Shares.IsPositive() {
			summary.TotalNonRegACB = summary.TotalNonRegACB.Add(acb.TotalACB)
			if pos.HasPrice {
				summary.TotalUnrealizedCAD = summary.TotalUnrealizedCAD.Add(pos.UnrealizedGainCAD)
			}
		}

		positions = append(positions, pos)
	}

	slices.SortFunc(positions, func(a, b PortfolioPosition) int {
		return strings.Compare(a.Security.Ticker, b.Security.Ticker)
	})
	return positions, summary, nil
}

// netShares computes net share count from a slice of transactions across any accounts.
func netShares(txs []*transaction.Transaction) decimal.Decimal {
	var shares decimal.Decimal
	for _, tx := range txs {
		switch {
		case isAcquisitionType(tx.Type):
			shares = shares.Add(tx.Quantity)
		case isDisposalType(tx.Type):
			shares = shares.Sub(tx.Quantity)
		}
	}
	return shares
}
