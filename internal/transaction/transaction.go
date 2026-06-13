package transaction

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

type Type string

const (
	TypeBuy           Type = "buy"
	TypeSell          Type = "sell"
	TypeDividend      Type = "dividend"
	TypeROCAdjustment Type = "roc_adjustment"
	TypeFXConversion  Type = "fx_conversion"
	TypeTransferIn    Type = "transfer_in"
	TypeTransferOut   Type = "transfer_out"
	TypeJournal       Type = "journal"
)

type Source string

const (
	SourceManual       Source = "manual"
	SourceQuestrade    Source = "questrade"
	SourceCanaccordCSV Source = "canaccord_csv"
)

type Transaction struct {
	ID                  int64
	AccountID           int64
	SecurityID          int64
	Type                Type
	TradeDate           time.Time
	SettledDate         time.Time
	Quantity            decimal.Decimal
	PriceNative         decimal.Decimal // price in security's currency
	CommissionNative    decimal.Decimal
	FXRate              *decimal.Decimal // nil when security currency == CAD
	PriceCAD            decimal.Decimal
	CommissionCAD       decimal.Decimal
	Source              Source
	Notes               string
	LinkedTransactionID *int64 // pairs Norbert's Gambit legs
	CreatedAt           time.Time
}

type Store interface {
	Create(ctx context.Context, tx *Transaction) error
	GetByID(ctx context.Context, id int64) (*Transaction, error)
	ListByAccount(ctx context.Context, accountID int64) ([]*Transaction, error)
	// ListBySecurityNonRegistered returns all transactions for a security across
	// non-registered accounts — used for ACB calculation.
	ListBySecurityNonRegistered(ctx context.Context, securityID, userID int64) ([]*Transaction, error)
	// ListDistinctNonRegisteredSecurityIDsByUser returns the distinct security IDs with
	// a disposal in non-registered accounts for a user within [from, to], ordered by first
	// disposal date ascending. Used by GainsService to enumerate securities without fetching
	// full transaction rows.
	ListDistinctNonRegisteredSecurityIDsByUser(ctx context.Context, userID int64, from, to time.Time) ([]int64, error)
	// ListNonRegisteredDisposalsByUser returns all sell and transfer-out transactions
	// in non-registered accounts for a user within [from, to] — used to discover taxable dispositions.
	ListNonRegisteredDisposalsByUser(ctx context.Context, userID int64, from, to time.Time) ([]*Transaction, error)
	// ListBySecurityAllAccounts returns all transactions for a security across all
	// accounts (including registered) — used for superficial loss window checks.
	ListBySecurityAllAccounts(ctx context.Context, securityID, userID int64) ([]*Transaction, error)
	ListByDateRange(ctx context.Context, accountID int64, from, to time.Time) ([]*Transaction, error)
	Delete(ctx context.Context, id int64) error
	UpdateFXRate(ctx context.Context, id int64, fxRate *decimal.Decimal, priceCAD, commCAD decimal.Decimal) error
}
