package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/gordcurrie/pacioli/internal/account"
	"github.com/gordcurrie/pacioli/internal/errs"
	"github.com/gordcurrie/pacioli/internal/security"
)

// ownedAccountSet builds a set of account IDs owned by the user.
func ownedAccountSet(accounts []*account.Account) map[int64]bool {
	m := make(map[int64]bool, len(accounts))
	for _, a := range accounts {
		m[a.ID] = true
	}
	return m
}

// importSession bundles the per-request state shared by every commit handler
// (questradeCommit, importCommit): the caller's owned accounts (for ownership
// checks on each row), a memoizing security lookup, and a random ID tying all
// transactions created in this commit to one audit-log import batch.
type importSession struct {
	ownedAccounts map[int64]bool
	lookupSec     func(int64) (secCacheEntry, error)
	importID      string
}

func (h *Handler) newImportSession(ctx context.Context, userID int64) (importSession, error) {
	accounts, err := h.accounts.ListByUser(ctx, userID)
	if err != nil {
		return importSession{}, fmt.Errorf("new import session: list accounts: %w", err)
	}

	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return importSession{}, fmt.Errorf("new import session: generate import id: %w", err)
	}

	return importSession{
		ownedAccounts: ownedAccountSet(accounts),
		lookupSec:     makeSecLookup(ctx, h.securities),
		importID:      hex.EncodeToString(b[:]),
	}, nil
}

type secCacheEntry struct {
	currency string
	found    bool
}

// makeSecLookup returns a closure that fetches security metadata on first call
// and caches results for the duration of the request.
func makeSecLookup(ctx context.Context, store security.Store) func(int64) (secCacheEntry, error) {
	cache := make(map[int64]secCacheEntry)
	return func(id int64) (secCacheEntry, error) {
		if s, ok := cache[id]; ok {
			return s, nil
		}
		sec, err := store.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, errs.ErrNotFound) {
				cache[id] = secCacheEntry{}
				return secCacheEntry{}, nil
			}
			return secCacheEntry{}, fmt.Errorf("lookup security %d: %w", id, err)
		}
		s := secCacheEntry{currency: sec.Currency, found: true}
		cache[id] = s
		return s, nil
	}
}
