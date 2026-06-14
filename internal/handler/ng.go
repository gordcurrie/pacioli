package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/service"
)

type ngPreviewData struct {
	Pairs []service.NGPair
	Error string
}

func (h *Handler) ngPreview(w http.ResponseWriter, r *http.Request) {
	log := loggerFromCtx(r.Context())
	userID := userFromCtx(r.Context()).ID

	pairs, err := h.ngSvc.DetectPairs(r.Context(), userID)
	if err != nil {
		log.Error("ng detect pairs", "err", err)
		h.render(w, r, "ng_preview", ngPreviewData{Error: "Failed to detect pairs"})
		return
	}
	h.render(w, r, "ng_preview", ngPreviewData{Pairs: pairs})
}

func (h *Handler) ngLink(w http.ResponseWriter, r *http.Request) {
	log := loggerFromCtx(r.Context())
	userID := userFromCtx(r.Context()).ID

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/questrade?error=Invalid+form", http.StatusSeeOther)
		return
	}

	// Build the set of (giveID, recvID) pairs the user reviewed in ngPreview.
	giveStrs := r.Form["give_id"]
	recvStrs := r.Form["recv_id"]
	if len(giveStrs) != len(recvStrs) {
		http.Redirect(w, r, "/questrade?error=Malformed+pair+data", http.StatusSeeOther)
		return
	}

	type idPair struct{ give, recv int64 }
	reviewed := make(map[idPair]bool, len(giveStrs))
	for i := range giveStrs {
		g, err1 := strconv.ParseInt(giveStrs[i], 10, 64)
		rv, err2 := strconv.ParseInt(recvStrs[i], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		reviewed[idPair{g, rv}] = true
	}
	if len(reviewed) == 0 {
		http.Redirect(w, r, "/questrade?ng=0", http.StatusSeeOther)
		return
	}

	// Re-detect to get ownership-verified pairs, then filter to the reviewed set.
	// Pairs that appeared after the preview was rendered are excluded.
	all, err := h.ngSvc.DetectPairs(r.Context(), userID)
	if err != nil {
		log.Error("ng detect pairs for link", "err", err)
		http.Redirect(w, r, "/questrade?error=Failed+to+detect+pairs", http.StatusSeeOther)
		return
	}

	var toLink []service.NGPair
	for _, p := range all {
		if reviewed[idPair{p.GiveLeg.ID, p.ReceiveLeg.ID}] {
			toLink = append(toLink, p)
		}
	}
	if len(toLink) == 0 {
		http.Redirect(w, r, "/questrade?ng=0", http.StatusSeeOther)
		return
	}

	n, err := h.ngSvc.LinkPairs(r.Context(), toLink)
	for _, p := range toLink[:n] {
		h.logAudit(r, audit.ActionUpdate, audit.EntityTransaction, p.GiveLeg.ID, audit.SourceQuestrade, "")
	}
	if err != nil {
		log.Error("ng link pairs", "err", err)
		if n > 0 {
			http.Redirect(w, r, fmt.Sprintf("/questrade?ng=%d&error=Some+pairs+failed+to+link", n), http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/questrade?error=Failed+to+link+pairs", http.StatusSeeOther)
		}
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/questrade?ng=%d", n), http.StatusSeeOther)
}
