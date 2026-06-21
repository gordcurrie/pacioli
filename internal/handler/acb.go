package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gordcurrie/pacioli/internal/audit"
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

type acbDetailPageData struct {
	Security     *security.Security
	Result       *service.ACBResult
	Transactions []*transaction.Transaction
	Rows         []service.HistoryRow
}

type dashboardPageData struct {
	Positions []service.PortfolioPosition
	Summary   service.PortfolioSummary
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	positions, summary, err := h.portfolioSvc.Build(r.Context(), userFromCtx(r.Context()).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r, "index", dashboardPageData{Positions: positions, Summary: summary})
}

func (h *Handler) listACB(w http.ResponseWriter, r *http.Request) {
	positions, err := h.loadPositions(r)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r, "acb_list", acbListPageData{Positions: positions})
}

func (h *Handler) loadPositions(r *http.Request) ([]positionSummary, error) {
	userID := userFromCtx(r.Context()).ID
	secIDs, err := h.transactions.ListDistinctAllSecurityIDsByUser(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	securities, err := h.securities.GetByIDs(r.Context(), secIDs)
	if err != nil {
		return nil, err
	}
	var positions []positionSummary
	for _, s := range securities {
		result, err := h.acbSvc.Calculate(r.Context(), s.ID, userID)
		if err != nil {
			return nil, err
		}
		if result.Shares.IsPositive() {
			positions = append(positions, positionSummary{Security: s, ACB: result})
		}
	}
	return positions, nil
}

func (h *Handler) userOwnsSecurityID(ctx context.Context, userID, securityID int64) (bool, error) {
	secIDs, err := h.transactions.ListDistinctAllSecurityIDsByUser(ctx, userID)
	if err != nil {
		return false, err
	}
	return slices.Contains(secIDs, securityID), nil
}

func (h *Handler) showACB(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	owned, err := h.userOwnsSecurityID(r.Context(), userFromCtx(r.Context()).ID, id)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if !owned {
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

	allTxs, err := h.transactions.ListBySecurityAllAccounts(r.Context(), id, userFromCtx(r.Context()).ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	adj, _, _ := service.ComputeSuperficialAdjustments(id, txs, allTxs)
	result, rows := service.CalculateACBWithHistory(id, txs, adj)

	h.render(w, r, "acb", acbDetailPageData{
		Security:     sec,
		Result:       result,
		Transactions: txs,
		Rows:         rows,
	})
}

func (h *Handler) updateSecurityPrice(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	priceStr := r.FormValue("last_price_cad")
	if priceStr == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	price, err := decimal.NewFromString(priceStr)
	if err != nil || !price.IsPositive() {
		http.Error(w, "invalid price", http.StatusBadRequest)
		return
	}

	owned, err := h.userOwnsSecurityID(r.Context(), userFromCtx(r.Context()).ID, id)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if !owned {
		http.NotFound(w, r)
		return
	}

	sec, err := h.securities.GetByID(r.Context(), id)
	if err != nil {
		h.notFoundOrError(w, r, err)
		return
	}
	snapshot, _ := json.Marshal(sec)

	if err := h.securities.UpdatePrice(r.Context(), id, price, time.Now().UTC()); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.logAudit(r, audit.ActionUpdate, audit.EntitySecurity, id, audit.SourceManual, string(snapshot))

	http.Redirect(w, r, "/", http.StatusSeeOther)
}
