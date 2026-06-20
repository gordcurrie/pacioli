package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gordcurrie/pacioli/internal/audit"
)

func (h *Handler) refreshPrices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := loggerFromCtx(ctx)

	userID := userFromCtx(ctx).ID
	secIDs, err := h.transactions.DistinctAllSecurityIDsByUser(ctx, userID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	secs, err := h.securities.GetByIDs(ctx, secIDs)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	// Build a map so we can snapshot before-state per security.
	secByID := make(map[int64][]byte, len(secs))
	for _, s := range secs {
		if snap, err := json.Marshal(s); err == nil {
			secByID[s.ID] = snap
		}
	}

	results := h.yahooSvc.FetchPrices(ctx, secs)

	today := time.Now().UTC()
	var updated, failed int
	for _, res := range results {
		if res.Err != nil {
			log.Warn("yahoo price fetch", "ticker", res.Ticker, "err", res.Err)
			failed++
			continue
		}
		if !res.PriceCAD.IsPositive() {
			continue
		}
		if err := h.securities.UpdatePrice(ctx, res.SecurityID, res.PriceCAD, today); err != nil {
			log.Error("update security price", "security_id", res.SecurityID, "err", err)
			failed++
			continue
		}
		if snap := secByID[res.SecurityID]; len(snap) > 0 {
			h.logAudit(r, audit.ActionUpdate, audit.EntitySecurity, res.SecurityID, audit.SourceYahoo, string(snap))
		}
		updated++
	}

	log.Info("price refresh", "updated", updated, "failed", failed)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
