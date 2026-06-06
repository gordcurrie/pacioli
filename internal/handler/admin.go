package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gordcurrie/pacioli/internal/audit"
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

	if _, err := h.users.Create(r.Context(), &user.User{
		Email:        email,
		PasswordHash: string(hash),
		IsAdmin:      r.FormValue("is_admin") == "on",
	}); err != nil {
		renderErr("Could not create user — email may already be in use.")
		return
	}

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
		h.serverError(w, r, err)
		return
	}

	h.logAudit(r, audit.ActionDelete, audit.EntityUser, id, audit.SourceManual, string(snapshot))

	users, _ := h.users.List(r.Context())
	h.render(w, r, "admin_users", adminUsersPageData{Users: users, Success: "User deleted."})
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

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	if err := h.users.UpdatePassword(r.Context(), id, string(hash)); err != nil {
		h.notFoundOrError(w, r, err)
		return
	}

	// Invalidate all existing sessions so stolen cookies can't be reused.
	if err := h.sessions.DeleteByUserID(r.Context(), id); err != nil {
		loggerFromCtx(r.Context()).Error("delete sessions after password reset", "user_id", id, "err", err)
	}

	users, _ := h.users.List(r.Context())
	h.render(w, r, "admin_users", adminUsersPageData{Users: users, Success: "Password reset."})
}
