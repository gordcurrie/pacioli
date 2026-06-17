package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

// TransactionStore is the SQLite implementation of transaction.Store.
type TransactionStore struct {
	db *sql.DB
}

// NewTransactionStore constructs a TransactionStore backed by db.
func NewTransactionStore(db *sql.DB) *TransactionStore {
	return &TransactionStore{db: db}
}

func (r *TransactionStore) Create(ctx context.Context, tx *transaction.Transaction) error {
	var fxRate *string
	if tx.FXRate != nil {
		s := tx.FXRate.String()
		fxRate = &s
	}

	res, err := r.db.ExecContext(ctx,
		`INSERT INTO transactions
		 (account_id, security_id, type, trade_date, settled_date,
		  quantity, price_native, commission_native, fx_rate, price_cad, commission_cad,
		  source, notes, linked_transaction_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		tx.AccountID, tx.SecurityID, string(tx.Type),
		tx.TradeDate.Format(time.DateOnly), tx.SettledDate.Format(time.DateOnly),
		tx.Quantity.String(), tx.PriceNative.String(), tx.CommissionNative.String(),
		fxRate, tx.PriceCAD.String(), tx.CommissionCAD.String(),
		string(tx.Source), tx.Notes, tx.LinkedTransactionID,
	)
	if err != nil {
		return fmt.Errorf("create transaction: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("get transaction id: %w", err)
	}
	tx.ID = id
	return nil
}

func (r *TransactionStore) GetByID(ctx context.Context, id int64) (*transaction.Transaction, error) {
	row := r.db.QueryRowContext(ctx, txSelectSQL+" WHERE t.id=?", id)
	return scanTransaction(row)
}

func (r *TransactionStore) ListByAccount(ctx context.Context, accountID int64) ([]*transaction.Transaction, error) {
	rows, err := r.db.QueryContext(ctx, txSelectSQL+" WHERE t.account_id=? ORDER BY t.trade_date, t.id", accountID)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanTransactions(rows)
}

func (r *TransactionStore) ListBySecurityNonRegistered(ctx context.Context, securityID, userID int64) ([]*transaction.Transaction, error) {
	rows, err := r.db.QueryContext(ctx,
		txSelectSQL+` WHERE t.security_id=? AND a.user_id=? AND a.is_registered=0
		              ORDER BY t.trade_date, t.id`,
		securityID, userID)
	if err != nil {
		return nil, fmt.Errorf("list transactions for ACB: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanTransactions(rows)
}

func (r *TransactionStore) ListDistinctNonRegisteredSecurityIDsByUser(ctx context.Context, userID int64, from, to time.Time) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT t.security_id
		 FROM transactions t
		 JOIN accounts a ON a.id = t.account_id
		 WHERE a.user_id=? AND a.is_registered=0 AND t.type IN ('sell','transfer_out')
		   AND t.trade_date BETWEEN ? AND ?
		 GROUP BY t.security_id
		 ORDER BY MIN(t.trade_date), MIN(t.id)`,
		userID, from.Format(time.DateOnly), to.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("list distinct security IDs with disposals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan security ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list distinct security IDs with disposals: %w", err)
	}
	return ids, nil
}

func (r *TransactionStore) DistinctAllSecurityIDsByUser(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT DISTINCT t.security_id FROM transactions t
		 JOIN accounts a ON a.id = t.account_id
		 WHERE a.user_id=?
		 ORDER BY t.security_id`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("distinct security IDs by user: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan security ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("distinct security IDs by user: %w", err)
	}
	return ids, nil
}

func (r *TransactionStore) ListNonRegisteredDisposalsByUser(ctx context.Context, userID int64, from, to time.Time) ([]*transaction.Transaction, error) {
	rows, err := r.db.QueryContext(ctx,
		txSelectSQL+` WHERE a.user_id=? AND a.is_registered=0 AND t.type IN ('sell','transfer_out')
		              AND t.trade_date BETWEEN ? AND ?
		              ORDER BY t.trade_date, t.id`,
		userID, from.Format(time.DateOnly), to.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("list non-registered disposals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanTransactions(rows)
}

func (r *TransactionStore) ListBySecurityAllAccounts(ctx context.Context, securityID, userID int64) ([]*transaction.Transaction, error) {
	rows, err := r.db.QueryContext(ctx,
		txSelectSQL+` WHERE t.security_id=? AND a.user_id=?
		              ORDER BY t.trade_date, t.id`,
		securityID, userID)
	if err != nil {
		return nil, fmt.Errorf("list transactions all accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanTransactions(rows)
}

func (r *TransactionStore) ListByDateRange(ctx context.Context, accountID int64, from, to time.Time) ([]*transaction.Transaction, error) {
	rows, err := r.db.QueryContext(ctx,
		txSelectSQL+` WHERE t.account_id=? AND t.trade_date BETWEEN ? AND ?
		              ORDER BY t.trade_date, t.id`,
		accountID, from.Format(time.DateOnly), to.Format(time.DateOnly))
	if err != nil {
		return nil, fmt.Errorf("list transactions by date range: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanTransactions(rows)
}

func (r *TransactionStore) ListUnlinkedBySecurityAndType(ctx context.Context, securityID, userID int64, typ transaction.Type) ([]*transaction.Transaction, error) {
	rows, err := r.db.QueryContext(ctx,
		txSelectSQL+` WHERE t.security_id=? AND a.user_id=? AND t.type=? AND t.linked_transaction_id IS NULL
		              ORDER BY t.trade_date, t.id`,
		securityID, userID, string(typ))
	if err != nil {
		return nil, fmt.Errorf("list unlinked transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanTransactions(rows)
}

func (r *TransactionStore) LinkNorbertGambitPair(ctx context.Context, giveLegID, receiveLegID int64) error {
	dbTx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("link ng pair: begin: %w", err)
	}
	defer func() { _ = dbTx.Rollback() }()

	res, err := dbTx.ExecContext(ctx,
		`UPDATE transactions SET type='fx_conversion', linked_transaction_id=? WHERE id=? AND type='transfer_out' AND linked_transaction_id IS NULL`,
		receiveLegID, giveLegID)
	if err != nil {
		return fmt.Errorf("link ng pair: give leg: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("link ng pair: give leg %d already linked or wrong type", giveLegID)
	}

	res, err = dbTx.ExecContext(ctx,
		`UPDATE transactions SET linked_transaction_id=? WHERE id=? AND type='journal' AND linked_transaction_id IS NULL`,
		giveLegID, receiveLegID)
	if err != nil {
		return fmt.Errorf("link ng pair: receive leg: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("link ng pair: receive leg %d already linked", receiveLegID)
	}

	if err := dbTx.Commit(); err != nil {
		return fmt.Errorf("link ng pair: commit: %w", err)
	}
	return nil
}

func (r *TransactionStore) LinkNorbertGambitPairDirect(ctx context.Context, give, receive *transaction.Transaction) error {
	dbTx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("link ng pair direct: begin: %w", err)
	}
	defer func() { _ = dbTx.Rollback() }()

	var giveFXRate *string
	if give.FXRate != nil {
		s := give.FXRate.String()
		giveFXRate = &s
	}

	// Synthetic TypeFXConversion on give security: records disposal of give-leg shares,
	// reducing its ACB pool. Dated at the original buy date so it sorts after the buy.
	res, err := dbTx.ExecContext(ctx,
		`INSERT INTO transactions
		 (account_id, security_id, type, trade_date, settled_date,
		  quantity, price_native, commission_native, fx_rate, price_cad, commission_cad,
		  source, notes, linked_transaction_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,NULL)`,
		give.AccountID, give.SecurityID, string(transaction.TypeFXConversion),
		give.TradeDate.Format(time.DateOnly), give.TradeDate.Format(time.DateOnly),
		give.Quantity.String(), give.PriceNative.String(), "0",
		giveFXRate, give.PriceCAD.String(), "0",
		string(give.Source), "synthetic NG: journal not reported by broker",
	)
	if err != nil {
		return fmt.Errorf("link ng pair direct: create fx_conversion: %w", err)
	}
	fxConvID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("link ng pair direct: fx_conversion id: %w", err)
	}

	// Synthetic TypeJournal on receive security: establishes ACB basis before the sell.
	// PriceCAD = give.PriceCAD transfers the cost basis from the give-leg into the receive-leg
	// ACB pool. When the receive security is non-CAD (reverse NG: CAD→USD), we carry the
	// receive sell's FX rate forward so price_native is in the receive security's currency.
	// Dated at the give-leg date so it sorts before the TypeSell in the ACB engine.
	journalPriceNative := give.PriceCAD
	var journalFXRate *string
	if receive.FXRate != nil {
		journalPriceNative = give.PriceCAD.Div(*receive.FXRate)
		s := receive.FXRate.String()
		journalFXRate = &s
	}
	res, err = dbTx.ExecContext(ctx,
		`INSERT INTO transactions
		 (account_id, security_id, type, trade_date, settled_date,
		  quantity, price_native, commission_native, fx_rate, price_cad, commission_cad,
		  source, notes, linked_transaction_id)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		receive.AccountID, receive.SecurityID, string(transaction.TypeJournal),
		give.TradeDate.Format(time.DateOnly), give.TradeDate.Format(time.DateOnly),
		give.Quantity.String(), journalPriceNative.String(), "0",
		journalFXRate, give.PriceCAD.String(), "0",
		string(give.Source), "synthetic NG: journal not reported by broker",
		fxConvID,
	)
	if err != nil {
		return fmt.Errorf("link ng pair direct: create journal: %w", err)
	}
	journalID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("link ng pair direct: journal id: %w", err)
	}

	// Cross-link the two synthetic transactions.
	if _, err = dbTx.ExecContext(ctx,
		`UPDATE transactions SET linked_transaction_id=? WHERE id=?`, journalID, fxConvID); err != nil {
		return fmt.Errorf("link ng pair direct: link synthetics: %w", err)
	}

	// Mark original give (TypeBuy) and receive (TypeSell) as linked so they are excluded
	// from future NG detection (ListUnlinkedBySecurityAndType filters on IS NULL).
	if _, err = dbTx.ExecContext(ctx,
		`UPDATE transactions SET linked_transaction_id=? WHERE id=? AND linked_transaction_id IS NULL`,
		fxConvID, give.ID); err != nil {
		return fmt.Errorf("link ng pair direct: mark give: %w", err)
	}
	if _, err = dbTx.ExecContext(ctx,
		`UPDATE transactions SET linked_transaction_id=? WHERE id=? AND linked_transaction_id IS NULL`,
		journalID, receive.ID); err != nil {
		return fmt.Errorf("link ng pair direct: mark receive: %w", err)
	}

	if err := dbTx.Commit(); err != nil {
		return fmt.Errorf("link ng pair direct: commit: %w", err)
	}

	give.LinkedTransactionID = &fxConvID
	receive.LinkedTransactionID = &journalID
	return nil
}

func (r *TransactionStore) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM transactions WHERE id=?`, id)
	return err
}

func (r *TransactionStore) UpdateFXRate(ctx context.Context, id int64, fxRate *decimal.Decimal, priceCAD, commCAD decimal.Decimal) error {
	var fxStr *string
	if fxRate != nil {
		s := fxRate.String()
		fxStr = &s
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE transactions SET fx_rate=?, price_cad=?, commission_cad=? WHERE id=?`,
		fxStr, priceCAD.String(), commCAD.String(), id,
	)
	if err != nil {
		return fmt.Errorf("update transaction fx rate: %w", err)
	}
	return nil
}

const txSelectSQL = `
	SELECT t.id, t.account_id, t.security_id, t.type, t.trade_date, t.settled_date,
	       t.quantity, t.price_native, t.commission_native, t.fx_rate,
	       t.price_cad, t.commission_cad, t.source, t.notes, t.linked_transaction_id,
	       t.created_at
	FROM transactions t
	JOIN accounts a ON a.id = t.account_id`

type txScanner interface {
	Scan(dest ...any) error
}

func scanTransaction(s txScanner) (*transaction.Transaction, error) {
	var tx transaction.Transaction
	var txType, source string
	var tradeDate, settledDate, createdAt string
	var qty, priceNative, commNative, priceCAD, commCAD string
	var fxRate *string
	var linkedID *int64

	err := s.Scan(
		&tx.ID, &tx.AccountID, &tx.SecurityID, &txType, &tradeDate, &settledDate,
		&qty, &priceNative, &commNative, &fxRate,
		&priceCAD, &commCAD, &source, &tx.Notes, &linkedID,
		&createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan transaction: %w", err)
	}

	tx.Type = transaction.Type(txType)
	tx.Source = transaction.Source(source)
	tx.LinkedTransactionID = linkedID
	tx.TradeDate, _ = time.Parse(time.DateOnly, tradeDate)
	tx.SettledDate, _ = time.Parse(time.DateOnly, settledDate)
	tx.CreatedAt, _ = time.Parse(time.DateTime, createdAt)
	tx.Quantity, _ = decimal.NewFromString(qty)
	tx.PriceNative, _ = decimal.NewFromString(priceNative)
	tx.CommissionNative, _ = decimal.NewFromString(commNative)
	tx.PriceCAD, _ = decimal.NewFromString(priceCAD)
	tx.CommissionCAD, _ = decimal.NewFromString(commCAD)

	if fxRate != nil {
		r, _ := decimal.NewFromString(*fxRate)
		tx.FXRate = &r
	}

	return &tx, nil
}

func scanTransactions(rows *sql.Rows) ([]*transaction.Transaction, error) {
	var out []*transaction.Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tx)
	}
	return out, rows.Err()
}
