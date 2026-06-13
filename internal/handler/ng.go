package handler

import (
	"fmt"
	"net/http"

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
		h.render(w, r, "ng_preview", ngPreviewData{Error: "Failed to detect pairs: " + err.Error()})
		return
	}
	h.render(w, r, "ng_preview", ngPreviewData{Pairs: pairs})
}

func (h *Handler) ngLink(w http.ResponseWriter, r *http.Request) {
	log := loggerFromCtx(r.Context())
	userID := userFromCtx(r.Context()).ID

	pairs, err := h.ngSvc.DetectPairs(r.Context(), userID)
	if err != nil {
		log.Error("ng detect pairs for link", "err", err)
		http.Redirect(w, r, "/questrade?error=Failed+to+detect+pairs", http.StatusSeeOther)
		return
	}
	if len(pairs) == 0 {
		http.Redirect(w, r, "/questrade?ng=0", http.StatusSeeOther)
		return
	}

	n, err := h.ngSvc.LinkPairs(r.Context(), pairs)
	if err != nil {
		log.Error("ng link pairs", "err", err)
		http.Redirect(w, r, "/questrade?error=Failed+to+link+pairs", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/questrade?ng=%d", n), http.StatusSeeOther)
}
