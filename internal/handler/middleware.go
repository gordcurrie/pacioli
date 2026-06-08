package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/sqlite"
)

type contextKey int

const loggerKey contextKey = 0

// loggerFromCtx returns the request-scoped logger stored by RequestLogger middleware.
// Falls back to slog.Default() for non-request contexts.
func loggerFromCtx(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// statusWriter wraps http.ResponseWriter to capture the response status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) written() int {
	if sw.status == 0 {
		return http.StatusOK
	}
	return sw.status
}

// RequestLogger returns middleware that logs each request with method, path,
// status, latency, and a unique request_id.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			var b [8]byte
			_, _ = rand.Read(b[:])
			reqID := hex.EncodeToString(b[:])
			log := logger.With("request_id", reqID)

			sw := &statusWriter{ResponseWriter: w}
			ctx := context.WithValue(r.Context(), loggerKey, log)

			next.ServeHTTP(sw, r.WithContext(ctx))

			log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.written(),
				"latency_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// RequireAuth authenticates the request via session cookie. Expired or missing
// sessions redirect to /login. Sessions with totp_verified=false (pending 2FA)
// redirect to /login/2fa. On success the user is stored in context.
func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		hash := sqlite.HashToken(cookie.Value)
		sess, err := h.sessions.GetByTokenHash(r.Context(), hash)
		if err != nil {
			if !errors.Is(err, errs.ErrNotFound) {
				loggerFromCtx(r.Context()).Error("session lookup", "err", err)
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
				loggerFromCtx(r.Context()).Error("user lookup", "err", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			// User was deleted — treat as expired session.
			http.SetCookie(w, h.sessionCookie("", -1))
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		if u.TOTPEnabled && !sess.TOTPVerified {
			http.Redirect(w, r, "/login/2fa", http.StatusSeeOther)
			return
		}

		_ = h.sessions.UpdateLastSeen(r.Context(), sess.ID)

		ctx := context.WithValue(r.Context(), ctxKeyUser{}, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin wraps RequireAuth and additionally enforces is_admin.
// Non-admin authenticated users receive 403.
func (h *Handler) RequireAdmin(next http.Handler) http.Handler {
	return h.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !userFromCtx(r.Context()).IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// SetupGate redirects to /setup when no users have been configured yet.
// Applied as the outermost middleware in main.go, before auth.
func (h *Handler) SetupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/setup" || p == "/login" || p == "/login/2fa" || p == "/logout" || p == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		n, err := h.users.CountConfigured(r.Context())
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if n == 0 {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

