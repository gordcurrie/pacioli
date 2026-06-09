package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/user"
	"golang.org/x/crypto/bcrypt"
)

type adminUsersPageData struct {
	Users   []*user.User
	Success string
	Error   string
}

func (h *Handler) adminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.users.List(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r, "admin_users", adminUsersPageData{Users: users})
}

func (h *Handler) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	renderErr := func(msg string) {
		users, _ := h.users.List(r.Context())
		h.render(w, r, "admin_users", adminUsersPageData{Users: users, Error: msg})
	}

	if email == "" || password == "" {
		renderErr("Email and password are required.")
		return
	}
	if len(password) < 8 {
		renderErr("Password must be at least 8 characters.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	newID, err := h.users.Create(r.Context(), &user.User{
		Email:        email,
		PasswordHash: string(hash),
		IsAdmin:      r.FormValue("is_admin") == "on",
	})
	if err != nil {
		renderErr("Could not create user — email may already be in use.")
		return
	}
	h.logAudit(r, audit.ActionCreate, audit.EntityUser, newID, audit.SourceManual, "")

	users, _ := h.users.List(r.Context())
	h.render(w, r, "admin_users", adminUsersPageData{Users: users, Success: "User created."})
}

func (h *Handler) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	actor := userFromCtx(r.Context())
	if id == actor.ID {
		users, _ := h.users.List(r.Context())
		h.render(w, r, "admin_users", adminUsersPageData{Users: users, Error: "Cannot delete your own account."})
		return
	}

	target, err := h.users.GetByID(r.Context(), id)
	if err != nil {
		h.notFoundOrError(w, r, err)
		return
	}

	// Snapshot only non-sensitive fields — PasswordHash and TOTPSecret must not persist in audit log.
	snapshot, _ := json.Marshal(struct {
		ID          int64  `json:"id"`
		Email       string `json:"email"`
		IsAdmin     bool   `json:"is_admin"`
		TOTPEnabled bool   `json:"totp_enabled"`
	}{target.ID, target.Email, target.IsAdmin, target.TOTPEnabled})
	if err := h.users.Delete(r.Context(), id); err != nil {
		if errors.Is(err, errs.ErrConstraint) {
			users, _ := h.users.List(r.Context())
			h.render(w, r, "admin_users", adminUsersPageData{Users: users, Error: "User has linked transactions and cannot be deleted."})
			return
		}
		h.serverError(w, r, err)
		return
	}

	h.logAudit(r, audit.ActionDelete, audit.EntityUser, id, audit.SourceManual, string(snapshot))
	// Invalidate the SetupGate cache so the next request re-checks CountConfigured.
	// Prevents the gate from staying permanently open if this was the last user.
	h.setupConfigured.Store(false)

	users, _ := h.users.List(r.Context())
	h.render(w, r, "admin_users", adminUsersPageData{Users: users, Success: "User deleted."})
}

const auditPageSize = 50

type adminAuditFilter struct {
	EntityType string
	Action     string
	UserID     int64
}

type adminAuditPageData struct {
	Entries    []*audit.Entry
	Users      []*user.User
	Filter     adminAuditFilter
	Total      int
	Page       int
	PageSize   int
	TotalPages int
	PrevURL    string
	NextURL    string
}

func (h *Handler) adminAuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := adminAuditFilter{
		EntityType: q.Get("entity_type"),
		Action:     q.Get("action"),
	}
	if uid := q.Get("user_id"); uid != "" {
		f.UserID, _ = strconv.ParseInt(uid, 10, 64)
	}
	page := 1
	if p := q.Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 1 {
			page = n
		}
	}

	lf := audit.ListFilter{
		EntityType: audit.EntityType(f.EntityType),
		Action:     audit.Action(f.Action),
		UserID:     f.UserID,
		Limit:      auditPageSize,
		Offset:     (page - 1) * auditPageSize,
	}

	entries, err := h.audits.List(r.Context(), lf)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	total, err := h.audits.Count(r.Context(), lf)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	users, err := h.users.List(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	totalPages := (total + auditPageSize - 1) / auditPageSize
	if totalPages < 1 {
		totalPages = 1
	}

	data := adminAuditPageData{
		Entries:    entries,
		Users:      users,
		Filter:     f,
		Total:      total,
		Page:       page,
		PageSize:   auditPageSize,
		TotalPages: totalPages,
	}
	if page > 1 {
		data.PrevURL = auditURL(f, page-1)
	}
	if page < totalPages {
		data.NextURL = auditURL(f, page+1)
	}

	h.render(w, r, "admin_audit", data)
}

func auditURL(f adminAuditFilter, page int) string {
	v := url.Values{}
	if f.EntityType != "" {
		v.Set("entity_type", f.EntityType)
	}
	if f.Action != "" {
		v.Set("action", f.Action)
	}
	if f.UserID != 0 {
		v.Set("user_id", strconv.FormatInt(f.UserID, 10))
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return "/admin/audit"
	}
	return "/admin/audit?" + v.Encode()
}

func (h *Handler) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	password := r.FormValue("password")

	renderErr := func(msg string) {
		users, _ := h.users.List(r.Context())
		h.render(w, r, "admin_users", adminUsersPageData{Users: users, Error: msg})
	}

	if len(password) < 8 {
		renderErr("Password must be at least 8 characters.")
		return
	}

	target, err := h.users.GetByID(r.Context(), id)
	if err != nil {
		h.notFoundOrError(w, r, err)
		return
	}
	snapshot, _ := json.Marshal(struct {
		ID          int64  `json:"id"`
		Email       string `json:"email"`
		IsAdmin     bool   `json:"is_admin"`
		TOTPEnabled bool   `json:"totp_enabled"`
	}{target.ID, target.Email, target.IsAdmin, target.TOTPEnabled})

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	if err := h.users.UpdatePassword(r.Context(), id, string(hash)); err != nil {
		h.notFoundOrError(w, r, err)
		return
	}

	// Invalidate all existing sessions — treat failure as hard error; don't claim
	// success if old session cookies couldn't be revoked.
	if err := h.sessions.DeleteByUserID(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.logAudit(r, audit.ActionUpdate, audit.EntityUser, id, audit.SourceManual, string(snapshot))

	users, _ := h.users.List(r.Context())
	h.render(w, r, "admin_users", adminUsersPageData{Users: users, Success: "Password reset."})
}
