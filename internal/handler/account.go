package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/audit"
)

type accountsPageData struct {
	Accounts []*account.Account
	Error    string
}

type accountFormData struct {
	Account *account.Account
	Types   []account.Type
	Error   string
}

var accountTypes = []account.Type{
	account.TypeMargin, account.TypeCash,
	account.TypeTFSA, account.TypeRRSP, account.TypeRESP,
	account.TypeLRSP, account.TypeSRSP,
}

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.accounts.ListByUser(r.Context(), h.userID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	h.render(w, "accounts", accountsPageData{Accounts: accounts})
}

func (h *Handler) newAccount(w http.ResponseWriter, r *http.Request) {
	h.render(w, "account_form", accountFormData{
		Account: &account.Account{Currency: "CAD"},
		Types:   accountTypes,
	})
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render(w, "account_form", accountFormData{
			Account: &account.Account{Currency: "CAD"},
			Types:   accountTypes,
			Error:   "invalid form data",
		})
		return
	}

	a := &account.Account{
		UserID:        h.userID,
		Name:          r.FormValue("name"),
		Type:          account.Type(r.FormValue("type")),
		Broker:        r.FormValue("broker"),
		Currency:      r.FormValue("currency"),
		AccountNumber: r.FormValue("account_number"),
		Source:        string(audit.SourceManual),
	}

	if err := h.accounts.Create(r.Context(), a); err != nil {
		h.render(w, "account_form", accountFormData{
			Account: a,
			Types:   accountTypes,
			Error:   "failed to create account",
		})
		return
	}
	h.logAudit(r, audit.ActionCreate, audit.EntityAccount, a.ID, audit.SourceManual, "")
	http.Redirect(w, r, "/accounts", http.StatusSeeOther)
}

func (h *Handler) editAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a, err := h.accounts.GetByID(r.Context(), id)
	if err != nil {
		h.notFoundOrError(w, r, err)
		return
	}
	if a.UserID != h.userID {
		http.NotFound(w, r)
		return
	}
	h.render(w, "account_form", accountFormData{Account: a, Types: accountTypes})
}

func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	existing, err := h.accounts.GetByID(r.Context(), id)
	if err != nil {
		h.notFoundOrError(w, r, err)
		return
	}
	if existing.UserID != h.userID {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	a := &account.Account{
		ID:            id,
		UserID:        h.userID,
		Name:          r.FormValue("name"),
		Type:          account.Type(r.FormValue("type")),
		Broker:        r.FormValue("broker"),
		Currency:      r.FormValue("currency"),
		AccountNumber: r.FormValue("account_number"),
	}

	if err := h.accounts.Update(r.Context(), a); err != nil {
		h.render(w, "account_form", accountFormData{
			Account: a,
			Types:   accountTypes,
			Error:   "failed to update account",
		})
		return
	}
	http.Redirect(w, r, "/accounts", http.StatusSeeOther)
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	existing, err := h.accounts.GetByID(r.Context(), id)
	if err != nil {
		h.notFoundOrError(w, r, err)
		return
	}
	if existing.UserID != h.userID {
		http.NotFound(w, r)
		return
	}
	snapshot, _ := json.Marshal(existing)
	if err := h.accounts.Delete(r.Context(), id); err != nil {
		h.serverError(w, r, err)
		return
	}
	h.logAudit(r, audit.ActionDelete, audit.EntityAccount, id, audit.Source(existing.Source), string(snapshot))
	w.WriteHeader(http.StatusOK)
}
