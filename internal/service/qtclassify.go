package service

import (
	"regexp"
	"strings"

	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

var (
	// "BOOK VALUE 12345.67" — total book value; divide by qty for per-share price
	reBookValue = regexp.MustCompile(`BOOK VALUE\s+([\d,]*\d+(?:\.\d+)?)`)
	// "EXTERNAL TRANSFER-IN AS OF date @ 14.76000" — per-unit price directly
	// Anchored to "AS OF" to avoid false matches on other uses of @ in descriptions.
	reAtPrice = regexp.MustCompile(`AS OF\s+\S+\s*@\s*(\d+(?:\.\d+)?)`)
)

// priceFromDescription extracts the per-share ACB from a Questrade TFI description.
// Two formats are supported:
//   - "BOOK VALUE 12345.67" (total; divided by qty)
//   - "EXTERNAL TRANSFER-IN AS OF date @ 14.76000" (per-unit directly)
//
// Returns zero if neither pattern matches or qty is zero.
func priceFromDescription(description string, qty decimal.Decimal) decimal.Decimal {
	if m := reBookValue.FindStringSubmatch(description); m != nil {
		s := strings.ReplaceAll(m[1], ",", "")
		if total, err := decimal.NewFromString(s); err == nil && total.IsPositive() && !qty.IsZero() {
			return total.Div(qty)
		}
	}
	if m := reAtPrice.FindStringSubmatch(description); m != nil {
		if p, err := decimal.NewFromString(m[1]); err == nil && p.IsPositive() {
			return p
		}
	}
	return decimal.Zero
}

// QTActivityStatus is the result of classifying a Questrade activity for import.
type QTActivityStatus int

const (
	QTActivityImport QTActivityStatus = iota
	QTActivitySkip
	QTActivityFlag
)

// ClassifyQTActivity maps a Questrade Activity to an import disposition.
// Returns (status, flagMessage, transactionType).
// flagMessage is non-empty only when status == QTActivityFlag.
// transactionType is empty when status != QTActivityImport.
func ClassifyQTActivity(a *questrade.Activity) (QTActivityStatus, string, transaction.Type) {
	switch strings.TrimSpace(a.Action) {
	case "Buy":
		return QTActivityImport, "", transaction.TypeBuy
	case "Sell":
		return QTActivityImport, "", transaction.TypeSell
	case "DIV", "INT":
		return QTActivityImport, "", transaction.TypeDividend
	case "REI", "DRI":
		// Dividend/distribution reinvestment: acquires shares, increases ACB.
		return QTActivityImport, "", transaction.TypeBuy
	case "CON", "WDR", "DEP", "TFO", "EXP", "BRW", "LFJ", "":
		return QTActivitySkip, "", ""
	case "TFI":
		if !a.Quantity.IsPositive() {
			return QTActivitySkip, "", ""
		}
		if !a.Price.IsZero() {
			return QTActivityImport, "", transaction.TypeTransferIn
		}
		// price=0 is normal for in-kind transfers; book value is in the description.
		if bv := priceFromDescription(a.Description, a.Quantity); bv.IsPositive() {
			a.Price = bv
			return QTActivityImport, "", transaction.TypeTransferIn
		}
		return QTActivityFlag, "transfer in — no book value; enter ACB manually — desc: " + a.Description, ""
	case "FXT":
		// Norbert's Gambit journal: positive qty = receive leg, negative = give leg.
		if a.Quantity.IsPositive() {
			return QTActivityImport, "", transaction.TypeJournal
		}
		if a.Quantity.IsNegative() {
			return QTActivityImport, "", transaction.TypeTransferOut
		}
		return QTActivityFlag, "FX conversion — zero quantity; enter manually", ""
	default:
		return QTActivityFlag, "unknown action: " + a.Action, ""
	}
}
