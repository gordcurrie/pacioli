package handler

import (
	"database/sql"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/transaction"
)

type Handler struct {
	accounts     account.Store
	securities   security.Store
	transactions transaction.Store
	audits       audit.Store
	qtTokens     questrade.Store
	bocSvc       *service.BOCFetcher
	acbSvc       *service.ACBService
	userID       int64
	logger       *slog.Logger
	tmpls        map[string]*template.Template
}

// Config holds all dependencies for the Handler.
type Config struct {
	Accounts     account.Store
	Securities   security.Store
	Transactions transaction.Store
	Audits       audit.Store
	QTTokens     questrade.Store
	BOCSvc       *service.BOCFetcher
	ACBSvc       *service.ACBService
	UserID       int64
	Logger       *slog.Logger
	TemplateFS   fs.FS
}

func New(cfg *Config) (*Handler, error) {
	h := &Handler{
		accounts:     cfg.Accounts,
		securities:   cfg.Securities,
		transactions: cfg.Transactions,
		audits:       cfg.Audits,
		qtTokens:     cfg.QTTokens,
		bocSvc:       cfg.BOCSvc,
		acbSvc:       cfg.ACBSvc,
		userID:       cfg.UserID,
		logger:       cfg.Logger,
	}
	if err := h.parseTemplates(cfg.TemplateFS); err != nil {
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
		"import", "import_preview",
		"questrade", "questrade_preview",
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

	mux.HandleFunc("GET /import", h.importPage)
	mux.HandleFunc("POST /import/preview", h.importPreview)
	mux.HandleFunc("POST /import/commit", h.importCommit)

	mux.HandleFunc("GET /questrade", h.questradePage)
	mux.HandleFunc("POST /questrade/connect", h.questradeConnect)
	mux.HandleFunc("POST /questrade/disconnect", h.questradeDisconnect)
	mux.HandleFunc("POST /questrade/preview", h.questradePreview)
	mux.HandleFunc("POST /questrade/commit", h.questradeCommit)
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

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request, err error) {
	loggerFromCtx(r.Context()).Error("handler error", "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func (h *Handler) notFoundOrError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	h.serverError(w, r, err)
}

func (h *Handler) logAudit(r *http.Request, action audit.Action, entity audit.EntityType, id int64, source audit.Source, snapshot string) {
	if err := h.audits.Log(r.Context(), &audit.Entry{
		UserID:     h.userID,
		Action:     action,
		EntityType: entity,
		EntityID:   id,
		Source:     source,
		Snapshot:   snapshot,
	}); err != nil {
		loggerFromCtx(r.Context()).Error("audit log", "err", err)
	}
}
