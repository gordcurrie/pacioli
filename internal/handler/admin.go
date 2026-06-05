package handler

import (
	"net/http"
	"strconv"

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
		h.serverError(w, r, err)
		return
	}

	users, _ := h.users.List(r.Context())
	h.render(w, r, "admin_users", adminUsersPageData{Users: users, Success: "Password reset."})
}
