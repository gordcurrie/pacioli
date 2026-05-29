package handler

import (
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
)

type Handler struct {
	accounts     account.Store
	securities   security.Store
	transactions transaction.Store
	acbSvc       *service.ACBService
	userID       int64
	logger       *slog.Logger
	tmpls        map[string]*template.Template
}

func New(
	accounts account.Store,
	securities security.Store,
	transactions transaction.Store,
	acbSvc *service.ACBService,
	userID int64,
	logger *slog.Logger,
	tmplFS fs.FS,
) (*Handler, error) {
	h := &Handler{
		accounts:     accounts,
		securities:   securities,
		transactions: transactions,
		acbSvc:       acbSvc,
		userID:       userID,
		logger:       logger,
	}
	if err := h.parseTemplates(tmplFS); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *Handler) parseTemplates(fsys fs.FS) error {
	pages := []string{
		"index", "accounts", "account_form",
		"securities", "security_form",
		"transactions", "transaction_form",
		"acb", "acb_list",
	}
	h.tmpls = make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		t, err := template.ParseFS(fsys, "templates/layout.html", "templates/"+p+".html")
		if err != nil {
			return err
		}
		h.tmpls[p] = t
	}
	return nil
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.index)

	mux.HandleFunc("GET /accounts", h.listAccounts)
	mux.HandleFunc("GET /accounts/new", h.newAccount)
	mux.HandleFunc("POST /accounts", h.createAccount)
	mux.HandleFunc("GET /accounts/{id}/edit", h.editAccount)
	mux.HandleFunc("POST /accounts/{id}", h.updateAccount)
	mux.HandleFunc("DELETE /accounts/{id}", h.deleteAccount)

	mux.HandleFunc("GET /securities", h.listSecurities)
	mux.HandleFunc("GET /securities/new", h.newSecurity)
	mux.HandleFunc("GET /securities/search", h.searchSecurities)
	mux.HandleFunc("POST /securities", h.createSecurity)

	mux.HandleFunc("GET /transactions", h.listTransactions)
	mux.HandleFunc("GET /transactions/new", h.newTransaction)
	mux.HandleFunc("POST /transactions", h.createTransaction)
	mux.HandleFunc("DELETE /transactions/{id}", h.deleteTransaction)

	mux.HandleFunc("GET /acb", h.listACB)
	mux.HandleFunc("GET /acb/{id}", h.showACB)
}

func (h *Handler) render(w http.ResponseWriter, page string, data any) {
	tmpl, ok := h.tmpls[page]
	if !ok {
		h.logger.Error("template not found", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		h.logger.Error("render template", "page", page, "err", err)
	}
}

func (h *Handler) serverError(w http.ResponseWriter, err error) {
	h.logger.Error("handler error", "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
