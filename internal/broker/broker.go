package broker

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

// RowStatus indicates how an imported row should be handled.
type RowStatus int

const (
	RowImport RowStatus = iota // importable; will be committed
	RowSkip                    // silently skipped (fees, notional, zero-amount)
	RowFlag                    // needs manual review
)

// ParsedRow is the result of parsing one CSV row.
type ParsedRow struct {
	Line         int
	Status       RowStatus
	FlagReason   string
	TxType       transaction.Type
	TradeDate    time.Time
	SettledDate  time.Time
	SecurityName string          // extracted from description
	AccountNo    string          // trimmed account number
	Quantity     decimal.Decimal // always positive
	Price        decimal.Decimal
	Commission   decimal.Decimal
	Notes        string
}

// CommitRow is the JSON payload passed from preview form to commit handler.
type CommitRow struct {
	TradeDate   string `json:"td"`
	SettledDate string `json:"sd"`
	AccountID   int64  `json:"aid"`
	SecurityID  int64  `json:"sid"`
	TxType      string `json:"t"`
	Quantity    string `json:"q"`
	Price       string `json:"p"`
	Commission  string `json:"c"`
	Notes       string `json:"n"`
}

// Profile describes how to parse a broker's CSV export.
type Profile interface {
	Name() string
	Source() audit.Source
	Parse(record []string) (*ParsedRow, error)
}

// All returns all registered broker profiles.
func All() []Profile {
	return []Profile{NewCanaccordProfile()}
}

// ByName returns a profile by name, or nil.
func ByName(name string) Profile {
	for _, p := range All() {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// ParseCSV reads all rows from r using profile p, skipping the header row.
func ParseCSV(r io.Reader, p Profile) ([]*ParsedRow, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true

	if _, err := cr.Read(); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	var rows []*ParsedRow
	line := 2
	for {
		record, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read line %d: %w", line, err)
		}
		row, err := p.Parse(record)
		if err != nil {
			rows = append(rows, &ParsedRow{
				Line:       line,
				Status:     RowFlag,
				FlagReason: fmt.Sprintf("parse error: %v", err),
			})
		} else {
			row.Line = line
			rows = append(rows, row)
		}
		line++
	}
	return rows, nil
}
