package account

import (
	"context"
	"time"
)

type Type string

const (
	TypeMargin Type = "margin"
	TypeCash   Type = "cash"
	TypeTFSA   Type = "tfsa"
	TypeRRSP   Type = "rrsp"
	TypeRESP   Type = "resp"
	TypeLRSP   Type = "lrsp"
	TypeSRSP   Type = "srsp"
)

func (t Type) IsRegistered() bool {
	switch t {
	case TypeTFSA, TypeRRSP, TypeRESP, TypeLRSP, TypeSRSP:
		return true
	case TypeMargin, TypeCash:
		return false
	}
	return false
}

type Account struct {
	ID            int64
	UserID        int64
	Name          string
	Type          Type
	Broker        string
	Currency      string
	AccountNumber string
	IsRegistered  bool
	CreatedAt     time.Time
}

type Store interface {
	Create(ctx context.Context, a *Account) error
	GetByID(ctx context.Context, id int64) (*Account, error)
	ListByUser(ctx context.Context, userID int64) ([]*Account, error)
	Update(ctx context.Context, a *Account) error
	Delete(ctx context.Context, id int64) error
}
