// Package transaction defines the Transaction domain type and Store interface.
// Transactions are the primary input to ACB calculation; their Type determines
// how each row affects the running share count and cost pool.
package transaction

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Type classifies a transaction for ACB and tax treatment.
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

// Source identifies the originating data source for a transaction.
type Source string

const (
	SourceManual       Source = "manual"
	SourceQuestrade    Source = "questrade"
	SourceCanaccordCSV Source = "canaccord_csv"
)

// Transaction records a single brokerage event affecting a security position.
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

// Store defines persistence operations for transactions.
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
	// ListUnlinkedBySecurityAndType returns all transactions for a security and user of the given
	// type that have no linked_transaction_id set — used for Norbert's Gambit pair detection.
	ListUnlinkedBySecurityAndType(ctx context.Context, securityID, userID int64, typ Type) ([]*Transaction, error)
	Delete(ctx context.Context, id int64) error
	UpdateFXRate(ctx context.Context, id int64, fxRate *decimal.Decimal, priceCAD, commCAD decimal.Decimal) error
	// LinkNorbertGambitPair atomically converts the give-leg to TypeFXConversion and sets
	// linked_transaction_id on both legs.
	LinkNorbertGambitPair(ctx context.Context, giveLegID, receiveLegID int64) error
	// LinkNorbertGambitPairDirect handles NG pairs where the broker did not report the
	// intermediate journal transactions (common for Cash accounts). It creates synthetic
	// TypeFXConversion and TypeJournal records representing the journal step, then marks
	// the original give/receive as linked so they are excluded from future detection.
	LinkNorbertGambitPairDirect(ctx context.Context, give, receive *Transaction) error
}
