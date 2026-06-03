package handler

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/security"
)


type securitiesPageData struct {
	Securities []*security.Security
	Query      string
	Error      string
}

type securityFormData struct {
	Security    *security.Security
	Types       []security.Type
	EditMode    bool
	QTConnected bool
	Error       string
}

var securityTypes = []security.Type{
	security.TypeEquity, security.TypeETF, security.TypeMutualFund,
}

var searchResultTmpl = template.Must(template.New("sr").Parse(`
{{- range .}}
<label style="display:block; padding:0.25rem 0; cursor:pointer">
  <input type="radio" name="security_id" value="{{.ID}}" required>
  {{.Ticker}} — {{.Name}} ({{.Exchange}})
</label>
{{- end}}`))

// qtLookupData is the model for qtLookupTmpl.
type qtLookupData struct {
	Description string
	Exchange    string
	Name        string
	SecType     string
	Currency    string
}

var qtLookupTmpl = template.Must(template.New("qtlookup").Parse(
	`<span id="qt-lookup-msg" style="color:var(--pico-color-green-550)">&#10003; {{.Description}}</span>` +
		`<input id="sec-exchange" type="text" name="exchange" value="{{.Exchange}}" required placeholder="e.g. TSX" hx-swap-oob="outerHTML:#sec-exchange">` +
		`<input id="sec-name" type="text" name="name" value="{{.Name}}" required placeholder="e.g. Vanguard S&P 500 ETF" hx-swap-oob="outerHTML:#sec-name">` +
		`<script>document.getElementById('sec-type').value='{{.SecType}}';document.getElementById('sec-currency').value='{{.Currency}}';</script>`,
))

func (h *Handler) listSecurities(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var securities []*security.Security
	var err error
	if q != "" {
		securities, err = h.securities.Search(r.Context(), q)
	} else {
		securities, err = h.securities.ListAll(r.Context())
	}
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, "securities", securitiesPageData{Securities: securities, Query: q})
}

func (h *Handler) newSecurity(w http.ResponseWriter, r *http.Request) {
	qtConnected := h.qtTokens != nil && h.isQTConnected(r)
	h.render(w, "security_form", securityFormData{
		Security:    &security.Security{Currency: "CAD"},
		Types:       securityTypes,
		QTConnected: qtConnected,
	})
}

func (h *Handler) editSecurity(w http.ResponseWriter, r *http.Request) {
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
	h.render(w, "security_form", securityFormData{
		Security:    sec,
		Types:       securityTypes,
		EditMode:    true,
		QTConnected: h.qtTokens != nil && h.isQTConnected(r),
	})
}

func (h *Handler) updateSecurity(w http.ResponseWriter, r *http.Request) {
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

	qtConnected := h.qtTokens != nil && h.isQTConnected(r)
	renderForm := func(s *security.Security, errMsg string) {
		h.render(w, "security_form", securityFormData{
			Security:    s,
			Types:       securityTypes,
			EditMode:    true,
			QTConnected: qtConnected,
			Error:       errMsg,
		})
	}

	if err := r.ParseForm(); err != nil {
		renderForm(sec, "invalid form data")
		return
	}

	secType := security.Type(r.FormValue("type"))
	var validType bool
	for _, t := range securityTypes {
		if t == secType {
			validType = true
			break
		}
	}
	if !validType {
		renderForm(sec, "invalid security type")
		return
	}

	currency := r.FormValue("currency")
	if !validCurrencies[currency] {
		renderForm(sec, "invalid currency")
		return
	}

	sec.Ticker = r.FormValue("ticker")
	sec.Exchange = r.FormValue("exchange")
	sec.Name = r.FormValue("name")
	sec.Type = secType
	sec.Currency = currency

	if err := h.securities.Update(r.Context(), sec); err != nil {
		renderForm(sec, "failed to update security")
		return
	}
	h.logAudit(r, audit.ActionUpdate, audit.EntitySecurity, sec.ID, audit.SourceManual, "")
	http.Redirect(w, r, "/securities", http.StatusSeeOther)
}

func (h *Handler) deleteSecurity(w http.ResponseWriter, r *http.Request) {
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
	snapshot, err := json.Marshal(sec)
	if err != nil {
		loggerFromCtx(r.Context()).Error("snapshot marshal", "entity", "security", "id", id, "err", err)
	}
	if err := h.securities.Delete(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			http.Error(w, "Security is referenced by existing transactions and cannot be deleted.", http.StatusConflict)
			return
		}
		h.serverError(w, r, err)
		return
	}
	h.logAudit(r, audit.ActionDelete, audit.EntitySecurity, id, audit.SourceManual, string(snapshot))
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) searchSecurities(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("security_search_input")
	if q == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}
	results, err := h.securities.Search(r.Context(), q)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if len(results) == 0 {
		_, _ = w.Write([]byte(`<p style="padding:0.5rem">No results</p>`))
		return
	}
	if err := searchResultTmpl.Execute(w, results); err != nil {
		loggerFromCtx(r.Context()).Error("render security search results", "err", err)
	}
}

// qtSymbolLookup calls Questrade symbol search and returns OOB swaps to fill the security form.
func (h *Handler) qtSymbolLookup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if h.qtTokens == nil {
		return
	}
	prefix := r.URL.Query().Get("ticker")
	if prefix == "" {
		prefix = r.URL.Query().Get("prefix")
	}
	if len(prefix) < 2 {
		_, _ = w.Write([]byte(`<span id="qt-lookup-msg"></span>`))
		return
	}

	token, err := h.activeToken(r)
	if err != nil {
		_, _ = w.Write([]byte(`<span id="qt-lookup-msg" style="color:var(--pico-color-red-500)">lookup unavailable</span>`))
		return
	}

	client := questrade.New(token)
	results, err := client.SymbolSearch(r.Context(), prefix)
	if err != nil || len(results) == 0 {
		_, _ = w.Write([]byte(`<span id="qt-lookup-msg">no match</span>`))
		return
	}

	sr := results[0]
	secType := mapQTSecurityType(sr.SecurityType)
	currency := sr.Currency
	if !validCurrencies[currency] {
		currency = "CAD"
	}

	if err := qtLookupTmpl.Execute(w, qtLookupData{
		Description: sr.Description,
		Exchange:    sr.Exchange,
		Name:        sr.Description,
		SecType:     string(secType),
		Currency:    currency,
	}); err != nil {
		loggerFromCtx(r.Context()).Error("render qt lookup", "err", err)
	}
}

// isQTConnected returns true if a token is stored for the current user.
func (h *Handler) isQTConnected(r *http.Request) bool {
	_, err := h.qtTokens.Get(r.Context(), h.userID)
	return err == nil
}

var validCurrencies = map[string]bool{"CAD": true, "USD": true}

func (h *Handler) createSecurity(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render(w, "security_form", securityFormData{
			Security: &security.Security{Currency: "CAD"},
			Types:    securityTypes,
			Error:    "invalid form data",
		})
		return
	}

	secType := security.Type(r.FormValue("type"))
	var validType bool
	for _, t := range securityTypes {
		if t == secType {
			validType = true
			break
		}
	}
	if !validType {
		h.render(w, "security_form", securityFormData{
			Security: &security.Security{Currency: "CAD"},
			Types:    securityTypes,
			Error:    "invalid security type",
		})
		return
	}

	currency := r.FormValue("currency")
	if !validCurrencies[currency] {
		h.render(w, "security_form", securityFormData{
			Security: &security.Security{Currency: "CAD"},
			Types:    securityTypes,
			Error:    "invalid currency",
		})
		return
	}

	s := &security.Security{
		Ticker:   r.FormValue("ticker"),
		Exchange: r.FormValue("exchange"),
		Name:     r.FormValue("name"),
		Type:     secType,
		Currency: currency,
		Source:   string(audit.SourceManual),
	}

	if err := h.securities.Create(r.Context(), s); err != nil {
		h.render(w, "security_form", securityFormData{
			Security: s,
			Types:    securityTypes,
			Error:    "failed to save security",
		})
		return
	}
	h.logAudit(r, audit.ActionCreate, audit.EntitySecurity, s.ID, audit.SourceManual, "")
	http.Redirect(w, r, "/securities", http.StatusSeeOther)
}

func mapQTSecurityType(qt string) security.Type {
	switch qt {
	case "Stock":
		return security.TypeEquity
	case "ETF":
		return security.TypeETF
	case "MutualFund":
		return security.TypeMutualFund
	default:
		return security.TypeEquity
	}
}
