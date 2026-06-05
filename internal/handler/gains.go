package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
)

type gainsDetailPageData struct {
	Year     int
	Security *security.Security
	History  []service.GainsDetailRow
}

type gainsPageData struct {
	Year     int
	PrevYear int
	NextYear int
	Report   *service.GainsReport
	Error    string
}

type rocPreviewPageData struct {
	Year  int
	Rows  []service.ROCPreviewRow
	Error string
}

func parseYear(r *http.Request) (int, bool) {
	year, err := strconv.Atoi(r.PathValue("year"))
	if err != nil || year < 1990 || year > 2100 {
		return 0, false
	}
	return year, true
}

func (h *Handler) listGains(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, fmt.Sprintf("/gains/%d", time.Now().Year()), http.StatusSeeOther)
}

func (h *Handler) showGainsForYear(w http.ResponseWriter, r *http.Request) {
	year, ok := parseYear(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	report, err := h.gainsSvc.Calculate(r.Context(), userFromCtx(r.Context()).ID, year)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r,"gains", gainsPageData{Year: year, PrevYear: year - 1, NextYear: year + 1, Report: report})
}

func (h *Handler) exportGainsCSV(w http.ResponseWriter, r *http.Request) {
	year, ok := parseYear(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	report, err := h.gainsSvc.Calculate(r.Context(), userFromCtx(r.Context()).ID, year)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="capital-gains-%d.csv"`, year))

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"Date", "Ticker", "Exchange", "Shares", "Proceeds (CAD)", "ACB at Sell (CAD)", "Gain/Loss (CAD)", "Superficial Loss"}); err != nil {
		h.serverError(w, r, err)
		return
	}
	for _, line := range report.Lines {
		superficial := ""
		if line.IsSuperficialLoss {
			superficial = "YES"
		}
		if err := cw.Write([]string{
			line.TradeDate.Format(time.DateOnly),
			line.Security.Ticker,
			line.Security.Exchange,
			line.Shares.StringFixed(4),
			line.ProceedsCAD.StringFixed(2),
			line.ACBAtSell.StringFixed(2),
			line.GainLoss.StringFixed(2),
			superficial,
		}); err != nil {
			h.serverError(w, r, err)
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		h.serverError(w, r, err)
	}
}

func (h *Handler) showGainsDetail(w http.ResponseWriter, r *http.Request) {
	year, ok := parseYear(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	secID, err := strconv.ParseInt(r.PathValue("security_id"), 10, 64)
	if err != nil || secID <= 0 {
		http.NotFound(w, r)
		return
	}

	sec, history, err := h.gainsSvc.HistoryForSecurity(r.Context(), secID, userFromCtx(r.Context()).ID, year)
	if err != nil {
		h.notFoundOrError(w, r, err)
		return
	}

	h.render(w, r,"gains_detail", gainsDetailPageData{Year: year, Security: sec, History: history})
}

func (h *Handler) previewROC(w http.ResponseWriter, r *http.Request) {
	year, ok := parseYear(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	rows, err := h.rocSvc.PreviewROC(r.Context(), userFromCtx(r.Context()).ID, year)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r,"roc_preview", rocPreviewPageData{Year: year, Rows: rows})
}

func (h *Handler) applyROC(w http.ResponseWriter, r *http.Request) {
	year, ok := parseYear(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if err := h.rocSvc.ApplyROC(r.Context(), userFromCtx(r.Context()).ID, year); err != nil {
		h.serverError(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/roc/%d", year), http.StatusSeeOther) //#nosec G710 -- year is a validated integer (1990–2100), not a user-controlled string
}
