package security

import "context"

type Type string

const (
	TypeEquity     Type = "equity"
	TypeETF        Type = "etf"
	TypeMutualFund Type = "mutual_fund"
	TypeOption     Type = "option"
)

type Security struct {
	ID       int64
	Ticker   string
	Exchange string
	Name     string
	Type     Type
	Currency string
}

type Store interface {
	Create(ctx context.Context, s *Security) error
	GetByID(ctx context.Context, id int64) (*Security, error)
	GetByTickerExchange(ctx context.Context, ticker, exchange string) (*Security, error)
	Search(ctx context.Context, query string) ([]*Security, error)
	ListAll(ctx context.Context) ([]*Security, error)
}
