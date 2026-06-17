// Package security defines the Security domain type (ticker, exchange, instrument type)
// and the Store interface for persistence and search.
package security

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Type classifies a security instrument.
type Type string

const (
	TypeEquity     Type = "equity"
	TypeETF        Type = "etf"
	TypeMutualFund Type = "mutual_fund"
	TypeOption     Type = "option"
)

// Security represents a tradeable instrument identified by ticker and exchange.
type Security struct {
	ID           int64
	Ticker       string
	Exchange     string
	Name         string
	Type         Type
	Currency     string
	Source       string
	LastPriceCAD *decimal.Decimal // nil when not set; price in CAD
	LastPriceDate *time.Time      // nil when no price set
}

// Store defines persistence operations for securities.
type Store interface {
	Create(ctx context.Context, s *Security) error
	GetByID(ctx context.Context, id int64) (*Security, error)
	GetByTickerExchange(ctx context.Context, ticker, exchange string) (*Security, error)
	Search(ctx context.Context, query string) ([]*Security, error)
	ListAll(ctx context.Context) ([]*Security, error)
	Update(ctx context.Context, s *Security) error
	UpdatePrice(ctx context.Context, id int64, priceCAD decimal.Decimal, date time.Time) error
	Delete(ctx context.Context, id int64) error
}
