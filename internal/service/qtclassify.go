package service

import (
	"strings"

	"github.com/gordcurrie/pacioli/internal/questrade"
	"github.com/gordcurrie/pacioli/internal/transaction"
)

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
		// In-kind transfer in: positive qty carries book value as ACB cost basis.
		// Zero qty = cash sweep — skip.
		if a.Quantity.IsPositive() {
			return QTActivityImport, "", transaction.TypeTransferIn
		}
		return QTActivitySkip, "", ""
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
