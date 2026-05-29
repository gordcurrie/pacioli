package handler

import (
	"html/template"
	"net/http"

	"github.com/gordcurrie/pacioli/internal/security"
)

type securitiesPageData struct {
	Securities []*security.Security
	Query      string
	Error      string
}

type securityFormData struct {
	Security *security.Security
	Types    []security.Type
	Error    string
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
		h.serverError(w, err)
		return
	}
	h.render(w, "securities", securitiesPageData{Securities: securities, Query: q})
}

func (h *Handler) newSecurity(w http.ResponseWriter, r *http.Request) {
	h.render(w, "security_form", securityFormData{
		Security: &security.Security{Currency: "CAD"},
		Types:    securityTypes,
	})
}

func (h *Handler) searchSecurities(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("security_search_input")
	if q == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}
	results, err := h.securities.Search(r.Context(), q)
	if err != nil {
		h.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if len(results) == 0 {
		_, _ = w.Write([]byte(`<p style="padding:0.5rem">No results</p>`))
		return
	}
	if err := searchResultTmpl.Execute(w, results); err != nil {
		h.logger.Error("render security search results", "err", err)
	}
}

func (h *Handler) createSecurity(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render(w, "security_form", securityFormData{
			Security: &security.Security{Currency: "CAD"},
			Types:    securityTypes,
			Error:    "invalid form data",
		})
		return
	}

	s := &security.Security{
		Ticker:   r.FormValue("ticker"),
		Exchange: r.FormValue("exchange"),
		Name:     r.FormValue("name"),
		Type:     security.Type(r.FormValue("type")),
		Currency: r.FormValue("currency"),
	}

	if err := h.securities.Create(r.Context(), s); err != nil {
		h.render(w, "security_form", securityFormData{
			Security: s,
			Types:    securityTypes,
			Error:    "failed to create security (may already exist)",
		})
		return
	}
	http.Redirect(w, r, "/securities", http.StatusSeeOther)
}
