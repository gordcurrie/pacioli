package handler

import (
	"net/http"
	"strconv"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
)

type positionSummary struct {
	Security *security.Security
	ACB      *service.ACBResult
}

type acbListPageData struct {
	Positions []positionSummary
}

type acbDetailPageData struct {
	Security     *security.Security
	Result       *service.ACBResult
	Transactions []*transaction.Transaction
	Rows         []service.HistoryRow
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	positions, err := h.loadPositions(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r,"index", acbListPageData{Positions: positions})
}

func (h *Handler) listACB(w http.ResponseWriter, r *http.Request) {
	positions, err := h.loadPositions(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r,"acb_list", acbListPageData{Positions: positions})
}

func (h *Handler) loadPositions(r *http.Request) ([]positionSummary, error) {
	securities, err := h.securities.ListAll(r.Context())
	if err != nil {
		return nil, err
	}
	var positions []positionSummary
	for _, s := range securities {
		result, err := h.acbSvc.Calculate(r.Context(), s.ID, userFromCtx(r.Context()).ID)
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
		h.notFoundOrError(w, r, err)
		return
	}

	txs, err := h.transactions.ListBySecurityNonRegistered(r.Context(), id, userFromCtx(r.Context()).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	result, rows := service.CalculateACBWithHistory(id, txs, nil)

	h.render(w, r,"acb", acbDetailPageData{
		Security:     sec,
		Result:       result,
		Transactions: txs,
		Rows:         rows,
	})
}
