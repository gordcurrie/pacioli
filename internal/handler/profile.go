package handler

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"image/png"
	"net/http"
	"strings"

	"github.com/gordcurrie/pacioli/internal/sqlite"
	"github.com/gordcurrie/pacioli/internal/user"
	"github.com/pquerna/otp"
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
	TOTPEnabled   bool
	QRDataURI     string
	PendingToken  string // AES-encrypted "secret::userID" — server-bound, tamper-proof
	RecoveryCount int    // unused codes remaining
	RecoveryCodes []string
	Error         string
	TOTPDisabled  bool // flash after disable
	EncKeyMissing bool
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
		qrURI, err := keyToQRDataURI(key)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		// Encrypt "secret::userID" so the browser-round-tripped token is server-bound.
		// An attacker cannot substitute an arbitrary secret without forging this ciphertext.
		tok, err := sqlite.Encrypt(h.encKey, fmt.Sprintf("%s::%d", key.Secret(), u.ID))
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		data.QRDataURI = qrURI
		data.PendingToken = tok
	}

	h.render(w, r, "profile_2fa", data)
}

func (h *Handler) totpEnable(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r.Context())

	if len(h.encKey) != 32 {
		h.render(w, r, "profile_2fa", totpSetupPageData{EncKeyMissing: true})
		return
	}

	// Decrypt and validate the server-bound token.
	tok := r.FormValue("pending_token")
	if tok == "" {
		h.render(w, r, "profile_2fa", totpSetupPageData{Error: "Invalid request. Please start over."})
		return
	}
	plaintext, err := sqlite.Decrypt(h.encKey, tok)
	if err != nil {
		h.render(w, r, "profile_2fa", totpSetupPageData{Error: "Invalid request. Please start over."})
		return
	}
	parts := strings.SplitN(plaintext, "::", 2)
	if len(parts) != 2 || parts[1] != fmt.Sprintf("%d", u.ID) {
		h.render(w, r, "profile_2fa", totpSetupPageData{Error: "Invalid request. Please start over."})
		return
	}
	secret := parts[0]
	code := r.FormValue("code")

	if !totp.Validate(code, secret) {
		// Re-generate QR for the same secret so the user can retry.
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
		qrURI, err := keyToQRDataURI(key)
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		retryTok, err := sqlite.Encrypt(h.encKey, fmt.Sprintf("%s::%d", secret, u.ID))
		if err != nil {
			h.serverError(w, r, err)
			return
		}
		h.render(w, r, "profile_2fa", totpSetupPageData{
			QRDataURI:    qrURI,
			PendingToken: retryTok,
			Error:        "Invalid code. Scan the QR again and try once more.",
		})
		return
	}

	if err := h.users.UpdateTOTP(r.Context(), u.ID, secret, true); err != nil {
		h.serverError(w, r, err)
		return
	}

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
// Both forms use the formatted "XXXXX-XXXXX" string so bcrypt.CompareHashAndPassword
// works correctly against what the user types.
func generateRecoveryCodes(userID int64) ([]string, []*user.RecoveryCode, error) {
	plain := make([]string, 10)
	codes := make([]*user.RecoveryCode, 10)
	for i := range plain {
		b := make([]byte, 5)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, fmt.Errorf("generate recovery code: %w", err)
		}
		p := fmt.Sprintf("%X", b)
		formatted := p[:5] + "-" + p[5:] // always 11 chars: XXXXX-XXXXX
		plain[i] = formatted
		// Hash the formatted string — exactly what the user will enter.
		hash, err := bcrypt.GenerateFromPassword([]byte(formatted), bcryptCost)
		if err != nil {
			return nil, nil, fmt.Errorf("hash recovery code: %w", err)
		}
		codes[i] = &user.RecoveryCode{UserID: userID, Hash: string(hash)}
	}
	return plain, codes, nil
}

// keyToQRDataURI renders a TOTP key's QR code as a PNG data URI.
func keyToQRDataURI(key *otp.Key) (string, error) {
	img, err := key.Image(200, 200)
	if err != nil {
		return "", fmt.Errorf("totp key image: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("totp key png: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
