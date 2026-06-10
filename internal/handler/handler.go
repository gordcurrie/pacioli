package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/security"
	"github.com/gordcurrie/pacioli/internal/service"
	"github.com/gordcurrie/pacioli/internal/session"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/gordcurrie/pacioli/internal/user"
)

type Handler struct {
	accounts     account.Store
	securities   security.Store
	transactions transaction.Store
	audits       audit.Store
	qtTokens     questrade.Store
	users        user.Store
	sessions     session.Store
	bocSvc       *service.BOCFetcher
	acbSvc       *service.ACBService
	gainsSvc     *service.GainsService
	rocSvc       *service.ROCService
	encKey          []byte // AES-256 key for TOTP secret encryption; nil = TOTP disabled
	secureCookie    bool
	setupConfigured atomic.Bool // cached once CountConfigured > 0; avoids per-request DB query
	logger          *slog.Logger
	tmpls           map[string]*template.Template
	tokenMu         sync.Mutex // guards single-use refresh token exchange
}

// Config holds all dependencies for the Handler.
type Config struct {
	Accounts     account.Store
	Securities   security.Store
	Transactions transaction.Store
	Audits       audit.Store
	QTTokens     questrade.Store
	Users        user.Store
	Sessions     session.Store
	BOCSvc       *service.BOCFetcher
	ACBSvc       *service.ACBService
	GainsSvc     *service.GainsService
	ROCSvc       *service.ROCService
	EncKey       []byte
	SecureCookie bool
	Logger       *slog.Logger
	TemplateFS   fs.FS
}

func (cfg *Config) validate() error {
	switch {
	case cfg.Accounts == nil:
		return fmt.Errorf("handler: Accounts is required")
	case cfg.Securities == nil:
		return fmt.Errorf("handler: Securities is required")
	case cfg.Transactions == nil:
		return fmt.Errorf("handler: Transactions is required")
	case cfg.Audits == nil:
		return fmt.Errorf("handler: Audits is required")
	case cfg.Users == nil:
		return fmt.Errorf("handler: Users is required")
	case cfg.Sessions == nil:
		return fmt.Errorf("handler: Sessions is required")
	case cfg.ACBSvc == nil:
		return fmt.Errorf("handler: ACBSvc is required")
	case cfg.GainsSvc == nil:
		return fmt.Errorf("handler: GainsSvc is required")
	case cfg.ROCSvc == nil:
		return fmt.Errorf("handler: ROCSvc is required")
	case cfg.Logger == nil:
		return fmt.Errorf("handler: Logger is required")
	case cfg.TemplateFS == nil:
		return fmt.Errorf("handler: TemplateFS is required")
	case cfg.QTTokens != nil && cfg.BOCSvc == nil:
		return fmt.Errorf("handler: BOCSvc is required when QTTokens is configured")
	}
	return nil
}

func New(cfg *Config) (*Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("handler: nil config")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	h := &Handler{
		accounts:     cfg.Accounts,
		securities:   cfg.Securities,
		transactions: cfg.Transactions,
		audits:       cfg.Audits,
		qtTokens:     cfg.QTTokens,
		users:        cfg.Users,
		sessions:     cfg.Sessions,
		bocSvc:       cfg.BOCSvc,
		acbSvc:       cfg.ACBSvc,
		gainsSvc:     cfg.GainsSvc,
		rocSvc:       cfg.ROCSvc,
		encKey:       cfg.EncKey,
		secureCookie: cfg.SecureCookie,
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
		"gains",
		"gains_detail",
		"roc_preview",
		"import", "import_preview",
		"questrade", "questrade_preview",
		"login", "login_2fa", "setup",
		"admin_users", "admin_audit",
		"profile_password", "profile_2fa",
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
	// Public routes (no auth required)
	mux.HandleFunc("GET /setup", h.setupPage)
	mux.HandleFunc("POST /setup", h.setupSubmit)
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.loginSubmit)
	mux.HandleFunc("GET /login/2fa", h.totpPage)
	mux.HandleFunc("POST /login/2fa", h.totpSubmit)
	mux.HandleFunc("POST /logout", h.logout)

	auth := h.RequireAuth
	admin := h.RequireAdmin

	mux.Handle("GET /{$}", auth(http.HandlerFunc(h.index)))

	mux.Handle("GET /accounts", auth(http.HandlerFunc(h.listAccounts)))
	mux.Handle("GET /accounts/new", auth(http.HandlerFunc(h.newAccount)))
	mux.Handle("POST /accounts", auth(http.HandlerFunc(h.createAccount)))
	mux.Handle("GET /accounts/{id}/edit", auth(http.HandlerFunc(h.editAccount)))
	mux.Handle("POST /accounts/{id}", auth(http.HandlerFunc(h.updateAccount)))
	mux.Handle("DELETE /accounts/{id}", auth(http.HandlerFunc(h.deleteAccount)))

	mux.Handle("GET /securities", auth(http.HandlerFunc(h.listSecurities)))
	mux.Handle("GET /securities/new", auth(http.HandlerFunc(h.newSecurity)))
	mux.Handle("GET /securities/search", auth(http.HandlerFunc(h.searchSecurities)))
	mux.Handle("GET /securities/qt-lookup", auth(http.HandlerFunc(h.qtSymbolLookup)))
	mux.Handle("POST /securities", auth(http.HandlerFunc(h.createSecurity)))
	mux.Handle("GET /securities/{id}/edit", auth(http.HandlerFunc(h.editSecurity)))
	mux.Handle("POST /securities/{id}", auth(http.HandlerFunc(h.updateSecurity)))
	mux.Handle("DELETE /securities/{id}", auth(http.HandlerFunc(h.deleteSecurity)))

	mux.Handle("GET /transactions", auth(http.HandlerFunc(h.listTransactions)))
	mux.Handle("GET /transactions/new", auth(http.HandlerFunc(h.newTransaction)))
	mux.Handle("POST /transactions", auth(http.HandlerFunc(h.createTransaction)))
	mux.Handle("DELETE /transactions/{id}", auth(http.HandlerFunc(h.deleteTransaction)))
	mux.Handle("GET /transactions/{id}/fx/edit", auth(http.HandlerFunc(h.editTransactionFXForm)))
	mux.Handle("GET /transactions/{id}/fx/cell", auth(http.HandlerFunc(h.transactionFXCell)))
	mux.Handle("POST /transactions/{id}/fx", auth(http.HandlerFunc(h.updateTransactionFX)))

	mux.Handle("GET /acb", auth(http.HandlerFunc(h.listACB)))
	mux.Handle("GET /acb/{id}", auth(http.HandlerFunc(h.showACB)))

	mux.Handle("GET /gains", auth(http.HandlerFunc(h.listGains)))
	mux.Handle("GET /gains/{year}", auth(http.HandlerFunc(h.showGainsForYear)))
	mux.Handle("GET /gains/{year}/export", auth(http.HandlerFunc(h.exportGainsCSV)))
	mux.Handle("GET /gains/{year}/{security_id}", auth(http.HandlerFunc(h.showGainsDetail)))
	mux.Handle("GET /roc/{year}", auth(http.HandlerFunc(h.previewROC)))
	mux.Handle("POST /roc/{year}", auth(http.HandlerFunc(h.applyROC)))

	mux.Handle("GET /import", auth(http.HandlerFunc(h.importPage)))
	mux.Handle("POST /import/preview", auth(http.HandlerFunc(h.importPreview)))
	mux.Handle("POST /import/commit", auth(http.HandlerFunc(h.importCommit)))

	mux.Handle("GET /questrade", auth(http.HandlerFunc(h.questradePage)))
	mux.Handle("POST /questrade/connect", auth(http.HandlerFunc(h.questradeConnect)))
	mux.Handle("POST /questrade/disconnect", auth(http.HandlerFunc(h.questradeDisconnect)))
	mux.Handle("POST /questrade/sync", auth(http.HandlerFunc(h.questradeSync)))
	mux.Handle("POST /questrade/preview", auth(http.HandlerFunc(h.questradePreview)))
	mux.Handle("POST /questrade/commit", auth(http.HandlerFunc(h.questradeCommit)))

	mux.Handle("GET /admin/users", admin(http.HandlerFunc(h.adminListUsers)))
	mux.Handle("POST /admin/users", admin(http.HandlerFunc(h.adminCreateUser)))
	mux.Handle("POST /admin/users/{id}/reset-password", admin(http.HandlerFunc(h.adminResetPassword)))
	mux.Handle("POST /admin/users/{id}/delete", admin(http.HandlerFunc(h.adminDeleteUser)))
	mux.Handle("GET /admin/audit", admin(http.HandlerFunc(h.adminAuditLog)))

	mux.Handle("GET /profile/password", auth(http.HandlerFunc(h.passwordPage)))
	mux.Handle("POST /profile/password", auth(http.HandlerFunc(h.updatePassword)))
	mux.Handle("GET /profile/2fa", auth(http.HandlerFunc(h.totpSetupPage)))
	mux.Handle("POST /profile/2fa/enable", auth(http.HandlerFunc(h.totpEnable)))
	mux.Handle("POST /profile/2fa/disable", auth(http.HandlerFunc(h.totpDisable)))
}

// layoutData is passed to every full-page template execution so layout.html
// can render the current user in the nav without touching page-specific data.
type layoutData struct {
	CurrentUser *user.User
	Data        any
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, page string, data any) {
	tmpl, ok := h.tmpls[page]
	if !ok {
		h.logger.Error("template not found", "page", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	u, _ := r.Context().Value(ctxKeyUser{}).(*user.User)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", layoutData{CurrentUser: u, Data: data}); err != nil {
		h.logger.Error("render template", "page", page, "err", err)
	}
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request, err error) {
	loggerFromCtx(r.Context()).Error("handler error", "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func (h *Handler) notFoundOrError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, errs.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	h.serverError(w, r, err)
}

func (h *Handler) logAudit(r *http.Request, action audit.Action, entity audit.EntityType, id int64, source audit.Source, snapshot string) {
	u := userFromCtx(r.Context())
	if err := h.audits.Log(r.Context(), &audit.Entry{
		UserID:     u.ID,
		UserEmail:  u.Email,
		Action:     action,
		EntityType: entity,
		EntityID:   id,
		Source:     source,
		Snapshot:   snapshot,
	}); err != nil {
		loggerFromCtx(r.Context()).Error("audit log", "err", err)
	}
}

func (h *Handler) sessionCookie(raw string, maxAge int) *http.Cookie {
	return &http.Cookie{ //#nosec G124 -- Secure is configurable via SECURE_COOKIES env var; HttpOnly and SameSite=Strict are always set
		Name:     cookieName,
		Value:    raw,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteStrictMode,
	}
}

// context key for authenticated user
type ctxKeyUser struct{}

func userFromCtx(ctx context.Context) *user.User {
	u, ok := ctx.Value(ctxKeyUser{}).(*user.User)
	if !ok || u == nil {
		panic("userFromCtx: no user in context — RequireAuth middleware missing")
	}
	return u
}
