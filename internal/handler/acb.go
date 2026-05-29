package handler

import (
	"net/http"
	"strconv"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

type positionSummary struct {
	Security *security.Security
	ACB      *service.ACBResult
}

type acbListPageData struct {
	Positions []positionSummary
}

type acbHistoryRow struct {
	Tx                 *transaction.Transaction
	RunningShares      decimal.Decimal
	RunningACB         decimal.Decimal
	RunningACBPerShare decimal.Decimal
}

type acbDetailPageData struct {
	Security     *security.Security
	Result       *service.ACBResult
	Transactions []*transaction.Transaction
	Rows         []acbHistoryRow
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	positions, err := h.loadPositions(r)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.render(w, "index", acbListPageData{Positions: positions})
}

func (h *Handler) listACB(w http.ResponseWriter, r *http.Request) {
	positions, err := h.loadPositions(r)
	if err != nil {
		h.serverError(w, err)
		return
	}
	h.render(w, "acb_list", acbListPageData{Positions: positions})
}

func (h *Handler) loadPositions(r *http.Request) ([]positionSummary, error) {
	securities, err := h.securities.ListAll(r.Context())
	if err != nil {
		return nil, err
	}
	var positions []positionSummary
	for _, s := range securities {
		result, err := h.acbSvc.Calculate(r.Context(), s.ID, h.userID)
		if err != nil {
			return nil, err
		}
		if result.Shares.IsPositive() {
			positions = append(positions, positionSummary{Security: s, ACB: result})
		}
	}
	return positions, nil
}

func (h *Handler) showACB(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	sec, err := h.securities.GetByID(r.Context(), id)
	if err != nil {
		h.serverError(w, err)
		return
	}

	txs, err := h.transactions.ListBySecurityNonRegistered(r.Context(), id, h.userID)
	if err != nil {
		h.serverError(w, err)
		return
	}

	result := service.CalculateACB(id, txs)
	rows := buildHistoryRows(txs)

	h.render(w, "acb", acbDetailPageData{
		Security:     sec,
		Result:       result,
		Transactions: txs,
		Rows:         rows,
	})
}

func buildHistoryRows(txs []*transaction.Transaction) []acbHistoryRow {
	rows := make([]acbHistoryRow, 0, len(txs))
	var shares, totalACB decimal.Decimal
	for _, tx := range txs {
		switch tx.Type {
		case transaction.TypeBuy, transaction.TypeTransferIn, transaction.TypeJournal:
			cost := tx.Quantity.Mul(tx.PriceCAD).Add(tx.CommissionCAD)
			totalACB = totalACB.Add(cost)
			shares = shares.Add(tx.Quantity)
		case transaction.TypeSell, transaction.TypeTransferOut:
			if shares.IsPositive() {
				acbPerShare := totalACB.Div(shares)
				totalACB = totalACB.Sub(acbPerShare.Mul(tx.Quantity))
			}
			shares = shares.Sub(tx.Quantity)
		case transaction.TypeROCAdjustment:
			totalACB = totalACB.Sub(tx.Quantity.Mul(tx.PriceCAD))
		case transaction.TypeDividend, transaction.TypeFXConversion:
			// no ACB impact
		}

		var perShare decimal.Decimal
		if shares.IsPositive() {
			perShare = totalACB.Div(shares)
		}
		rows = append(rows, acbHistoryRow{
			Tx:                 tx,
			RunningShares:      shares,
			RunningACB:         totalACB,
			RunningACBPerShare: perShare,
		})
	}
	return rows
}
