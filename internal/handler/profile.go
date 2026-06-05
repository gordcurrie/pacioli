package handler

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"image/png"
	"net/http"

	"github.com/gordcurrie/pacioli/internal/user"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// --- Password change ---

type passwordPageData struct {
	Error   string
	Success string
}

func (h *Handler) passwordPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "profile_password", passwordPageData{})
}

func (h *Handler) updatePassword(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	current := r.FormValue("current_password")
	newPw := r.FormValue("password")
	confirm := r.FormValue("confirm")

	renderErr := func(msg string) {
		h.render(w, r, "profile_password", passwordPageData{Error: msg})
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(current)); err != nil {
		renderErr("Current password is incorrect.")
		return
	}
	if newPw != confirm {
		renderErr("Passwords do not match.")
		return
	}
	if len(newPw) < 8 {
		renderErr("Password must be at least 8 characters.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPw), bcryptCost)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if err := h.users.UpdatePassword(r.Context(), u.ID, string(hash)); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, r, "profile_password", passwordPageData{Success: "Password updated."})
}

// --- TOTP setup ---

type totpSetupPageData struct {
	TOTPEnabled    bool
	QRDataURI      string
	PendingSecret  string // hidden field, passed back on enable submit
	RecoveryCount  int    // unused codes remaining
	RecoveryCodes  []string
	Error          string
	TOTPDisabled   bool   // flash after disable
	EncKeyMissing  bool
}

func (h *Handler) totpSetupPage(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())

	data := totpSetupPageData{TOTPEnabled: u.TOTPEnabled}

	if len(h.encKey) != 32 {
		data.EncKeyMissing = true
		h.render(w, r, "profile_2fa", data)
		return
	}

	codes, _ := h.users.ListRecoveryCodes(r.Context(), u.ID)
	for _, c := range codes {
		if c.UsedAt == nil {
			data.RecoveryCount++
		}
	}

	if !u.TOTPEnabled {
		key, err := totp.Generate(totp.GenerateOpts{
			Issuer:      "Pacioli",
			AccountName: u.Email,
		})
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		img, err := key.Image(200, 200)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			h.serverError(w, r, err)
			return
		}
		data.QRDataURI = "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		data.PendingSecret = key.Secret()
	}

	h.render(w, r, "profile_2fa", data)
}

func (h *Handler) totpEnable(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())

	if len(h.encKey) != 32 {
		h.render(w, r, "profile_2fa", totpSetupPageData{EncKeyMissing: true})
		return
	}

	secret := r.FormValue("pending_secret")
	code := r.FormValue("code")

	if !totp.Validate(code, secret) {
		// Regenerate QR from the same secret so user can retry without starting over.
		rawSecret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		key, err := totp.Generate(totp.GenerateOpts{
			Issuer:      "Pacioli",
			AccountName: u.Email,
			Secret:      rawSecret,
		})
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		img, err := key.Image(200, 200)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		var buf bytes.Buffer
		_ = png.Encode(&buf, img)
		h.render(w, r, "profile_2fa", totpSetupPageData{
			QRDataURI:     "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
			PendingSecret: secret,
			Error:         "Invalid code. Scan the QR again and try once more.",
		})
		return
	}

	if err := h.users.UpdateTOTP(r.Context(), u.ID, secret, true); err != nil {
		h.serverError(w, r, err)
		return
	}

	// Generate 10 recovery codes.
	plainCodes, dbCodes, err := generateRecoveryCodes(u.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	_ = h.users.DeleteRecoveryCodes(r.Context(), u.ID)
	if err := h.users.CreateRecoveryCodes(r.Context(), dbCodes); err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, "profile_2fa", totpSetupPageData{
		TOTPEnabled:   true,
		RecoveryCodes: plainCodes,
	})
}

func (h *Handler) totpDisable(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())
	code := r.FormValue("code")

	if !u.TOTPEnabled {
		http.Redirect(w, r, "/profile/2fa", http.StatusSeeOther)
		return
	}
	if !totp.Validate(code, u.TOTPSecret) {
		h.render(w, r, "profile_2fa", totpSetupPageData{
			TOTPEnabled: true,
			Error:       "Invalid code.",
		})
		return
	}

	if err := h.users.UpdateTOTP(r.Context(), u.ID, "", false); err != nil {
		h.serverError(w, r, err)
		return
	}
	_ = h.users.DeleteRecoveryCodes(r.Context(), u.ID)

	h.render(w, r, "profile_2fa", totpSetupPageData{TOTPDisabled: true})
}

// generateRecoveryCodes produces 10 random codes (plain + bcrypt-hashed versions).
func generateRecoveryCodes(userID int64) ([]string, []*user.RecoveryCode, error) {
	plain := make([]string, 10)
	codes := make([]*user.RecoveryCode, 10)
	for i := range plain {
		b := make([]byte, 5)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, fmt.Errorf("generate recovery code: %w", err)
		}
		p := fmt.Sprintf("%X", b)
		// Format as XXXXX-XXXXX (10 hex chars, hyphen in middle)
		formatted := p[:5] + "-" + p[5:]
		plain[i] = formatted
		hash, err := bcrypt.GenerateFromPassword([]byte(p), bcryptCost)
		if err != nil {
			return nil, nil, fmt.Errorf("hash recovery code: %w", err)
		}
		codes[i] = &user.RecoveryCode{UserID: userID, Hash: string(hash)}
	}
	return plain, codes, nil
}
