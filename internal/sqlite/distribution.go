package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/gordcurrie/pacioli/internal/distribution"
	"github.com/shopspring/decimal"
)

// DistributionStore is the SQLite implementation of distribution.Store.
type DistributionStore struct {
	db *sql.DB
}

// NewDistributionStore constructs a DistributionStore backed by db.
func NewDistributionStore(db *sql.DB) *DistributionStore {
	return &DistributionStore{db: db}
}

func (r *DistributionStore) Upsert(ctx context.Context, d *distribution.Distribution) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO distributions (security_id, tax_year, roc_per_unit, total_distribution_per_unit, record_date, source, notes)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(security_id, tax_year) DO UPDATE SET
		   roc_per_unit=excluded.roc_per_unit,
		   total_distribution_per_unit=excluded.total_distribution_per_unit,
		   record_date=excluded.record_date,
		   source=excluded.source,
		   notes=excluded.notes`,
		d.SecurityID, d.TaxYear, d.ROCPerUnit.String(), d.TotalDistributionPerUnit.String(),
		d.RecordDate, d.Source, d.Notes,
	)
	return err
}

func (r *DistributionStore) GetBySecurityYear(ctx context.Context, securityID int64, taxYear int) (*distribution.Distribution, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, security_id, tax_year, roc_per_unit, total_distribution_per_unit, record_date, source, notes
		 FROM distributions WHERE security_id=? AND tax_year=?`,
		securityID, taxYear)
	return scanDistribution(row)
}

func (r *DistributionStore) ListBySecurity(ctx context.Context, securityID int64) ([]*distribution.Distribution, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, security_id, tax_year, roc_per_unit, total_distribution_per_unit, record_date, source, notes
		 FROM distributions WHERE security_id=? ORDER BY tax_year DESC`,
		securityID)
	if err != nil {
		return nil, fmt.Errorf("list distributions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*distribution.Distribution
	for rows.Next() {
		d, err := scanDistribution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *DistributionStore) ListByTaxYear(ctx context.Context, taxYear int) ([]*distribution.Distribution, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, security_id, tax_year, roc_per_unit, total_distribution_per_unit, record_date, source, notes
		 FROM distributions WHERE tax_year=? ORDER BY security_id`,
		taxYear)
	if err != nil {
		return nil, fmt.Errorf("list distributions by year: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*distribution.Distribution
	for rows.Next() {
		d, err := scanDistribution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type distScanner interface {
	Scan(dest ...any) error
}

func scanDistribution(s distScanner) (*distribution.Distribution, error) {
	var d distribution.Distribution
	var roc, total string
	if err := s.Scan(&d.ID, &d.SecurityID, &d.TaxYear, &roc, &total, &d.RecordDate, &d.Source, &d.Notes); err != nil {
		return nil, fmt.Errorf("scan distribution: %w", err)
	}
	d.ROCPerUnit, _ = decimal.NewFromString(roc)
	d.TotalDistributionPerUnit, _ = decimal.NewFromString(total)
	return &d, nil
}
