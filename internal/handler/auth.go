package handler

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/session"
	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/internal/user"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionDuration = 30 * 24 * time.Hour
	bcryptCost      = 12
	cookieName      = "pacioli_session"
)

// sentinelHash is computed once at startup and used to equalize the timing of
// failed login attempts where the email is not found. Without this, an attacker
// can enumerate registered addresses by measuring response latency: not-found
// returns in microseconds; wrong-password runs bcrypt (~100ms at cost 12).
var sentinelHash []byte

func init() {
	var err error
	sentinelHash, err = bcrypt.GenerateFromPassword([]byte("pacioli-sentinel"), bcryptCost)
	if err != nil {
		panic("failed to generate sentinel hash: " + err.Error())
	}
}

type setupPageData struct {
	Error string
}

// setupPage shows the first-run setup form. Redirects to /login if already configured.
func (h *Handler) setupPage(w http.ResponseWriter, r *http.Request) {
	n, err := h.users.CountConfigured(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	h.render(w, r, "setup", setupPageData{})
}

// setupSubmit handles first-run admin account creation.
// If an unconfigured user (no password_hash) already exists it is reused,
// preserving all linked accounts/transactions.
func (h *Handler) setupSubmit(w http.ResponseWriter, r *http.Request) {
	n, err := h.users.CountConfigured(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")
	confirm := r.FormValue("confirm")

	renderErr := func(msg string) { h.render(w, r, "setup", setupPageData{Error: msg}) }

	if email == "" || password == "" {
		renderErr("Email and password are required.")
		return
	}
	if password != confirm {
		renderErr("Passwords do not match.")
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

	// Reuse existing unconfigured user if present, else create new.
	existing, err := h.users.GetByEmail(r.Context(), email)
	var userID int64
	switch {
	case err == nil:
		// Only reuse users that have no password set — never overwrite a configured account.
		if existing.PasswordHash != "" {
			renderErr("An account with that email already exists.")
			return
		}
		// ConfigureUser atomically sets email, password, and is_admin in one UPDATE,
		// preventing partial-update races (e.g. password committed but admin flag not set).
		if err2 := h.users.ConfigureUser(r.Context(), existing.ID, email, string(hash), true); err2 != nil {
			h.serverError(w, r, err2)
			return
		}
		userID = existing.ID
	case errors.Is(err, errs.ErrNotFound):
		// No user with this email. Reuse any unconfigured user to preserve linked data
		// (e.g. accounts/transactions from a previous install that had no password set).
		unconfigured, err2 := h.users.GetFirstUnconfigured(r.Context())
		switch {
		case err2 == nil:
			if err3 := h.users.ConfigureUser(r.Context(), unconfigured.ID, email, string(hash), true); err3 != nil {
				h.serverError(w, r, err3)
				return
			}
			userID = unconfigured.ID
		case errors.Is(err2, errs.ErrNotFound):
			// Truly fresh install — create the first admin.
			id, err3 := h.users.Create(r.Context(), &user.User{Email: email, PasswordHash: string(hash), IsAdmin: true})
			if err3 != nil {
				h.serverError(w, r, err3)
				return
			}
			userID = id
		default:
			h.serverError(w, r, err2)
			return
		}
	default:
		h.serverError(w, r, err)
		return
	}

	raw, err := generateToken()
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if err := h.sessions.Create(r.Context(), &session.Session{
		UserID:       userID,
		TokenHash:    sqlite.HashToken(raw),
		TOTPVerified: true,
		ExpiresAt:    time.Now().Add(sessionDuration),
	}); err != nil {
		h.serverError(w, r, err)
		return
	}

	http.SetCookie(w, h.sessionCookie(raw, int(sessionDuration.Seconds())))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type loginPageData struct {
	Error string
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "login", loginPageData{})
}

func (h *Handler) loginSubmit(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	u, err := h.users.GetByEmail(r.Context(), email)
	if err != nil {
		if !errors.Is(err, errs.ErrNotFound) {
			h.serverError(w, r, err)
			return
		}
		// Equalize timing with the found-but-wrong-password path to prevent
		// email enumeration via response latency differences.
		_ = bcrypt.CompareHashAndPassword(sentinelHash, []byte(password))
		h.render(w, r, "login", loginPageData{Error: "Invalid email or password."})
		return
	}
	if u.PasswordHash == "" {
		_ = bcrypt.CompareHashAndPassword(sentinelHash, []byte(password))
		h.render(w, r, "login", loginPageData{Error: "Invalid email or password."})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		h.render(w, r, "login", loginPageData{Error: "Invalid email or password."})
		return
	}

	raw, err := generateToken()
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	totpVerified := !u.TOTPEnabled
	if err := h.sessions.Create(r.Context(), &session.Session{
		UserID:       u.ID,
		TokenHash:    sqlite.HashToken(raw),
		TOTPVerified: totpVerified,
		ExpiresAt:    time.Now().Add(sessionDuration),
	}); err != nil {
		h.serverError(w, r, err)
		return
	}

	http.SetCookie(w, h.sessionCookie(raw, int(sessionDuration.Seconds())))

	if u.TOTPEnabled {
		http.Redirect(w, r, "/login/2fa", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type totpPageData struct {
	Error string
}

func (h *Handler) totpPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "login_2fa", totpPageData{})
}

func (h *Handler) totpSubmit(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	hash := sqlite.HashToken(cookie.Value)
	sess, err := h.sessions.GetByTokenHash(r.Context(), hash)
	if err != nil {
		if !errors.Is(err, errs.ErrNotFound) {
			loggerFromCtx(r.Context()).Error("session lookup in totp submit", "err", err)
			http.SetCookie(w, h.sessionCookie("", -1))
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, h.sessionCookie("", -1))
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if time.Now().After(sess.ExpiresAt) {
		_ = h.sessions.Delete(r.Context(), sess.ID)
		http.SetCookie(w, h.sessionCookie("", -1))
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	u, err := h.users.GetByID(r.Context(), sess.UserID)
	if err != nil {
		if !errors.Is(err, errs.ErrNotFound) {
			loggerFromCtx(r.Context()).Error("user lookup in totp submit", "err", err)
			http.SetCookie(w, h.sessionCookie("", -1))
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		// User deleted — clear cookie and redirect to login.
		http.SetCookie(w, h.sessionCookie("", -1))
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	code := r.FormValue("code")

	// Try TOTP first.
	if u.TOTPEnabled && u.TOTPSecret != "" && totp.Validate(code, u.TOTPSecret) {
		if err := h.sessions.SetTOTPVerified(r.Context(), sess.ID); err != nil {
			h.serverError(w, r, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Try recovery codes.
	if ok, err := h.consumeRecoveryCode(r, u.ID, code); err != nil {
		h.serverError(w, r, err)
		return
	} else if ok {
		if err := h.sessions.SetTOTPVerified(r.Context(), sess.ID); err != nil {
			h.serverError(w, r, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Throttle failed attempts — limits brute-force against recovery code bcrypt chain.
	time.Sleep(500 * time.Millisecond)
	h.render(w, r, "login_2fa", totpPageData{Error: "Invalid code. Try again."})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		hash := sqlite.HashToken(cookie.Value)
		if sess, err := h.sessions.GetByTokenHash(r.Context(), hash); err == nil {
			_ = h.sessions.Delete(r.Context(), sess.ID)
		}
	}
	http.SetCookie(w, h.sessionCookie("", -1))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// consumeRecoveryCode checks the submitted code against unused recovery codes for userID.
// Returns true and marks the code used on match.
func (h *Handler) consumeRecoveryCode(r *http.Request, userID int64, code string) (bool, error) {
	codes, err := h.users.ListRecoveryCodes(r.Context(), userID)
	if err != nil {
		return false, err
	}
	for _, rc := range codes {
		if err := bcrypt.CompareHashAndPassword([]byte(rc.Hash), []byte(code)); err == nil {
			if err := h.users.MarkRecoveryCodeUsed(r.Context(), rc.ID); err != nil {
				if errors.Is(err, errs.ErrNotFound) {
					continue // race: another concurrent request already consumed this code
				}
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

