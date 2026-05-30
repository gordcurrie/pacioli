package broker

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gordcurrie/pacioli/internal/audit"
	"github.com/gordcurrie/pacioli/internal/transaction"
	"github.com/shopspring/decimal"
)

// Canaccord CSV columns:
//   0:Date  1:Type  2:Description  3:Settlement  4:Quantity  5:Price  6:Amount  7:Account  8:Client Name  9:Client ID

const canaccordDateFmt = "1/2/2006 3:04:05 PM"

var trdSuffix = regexp.MustCompile(` TRD #\d+$`)

type canaccordProfile struct{}

func NewCanaccordProfile() Profile { return &canaccordProfile{} }

func (*canaccordProfile) Name() string         { return "Canaccord Genuity" }
func (*canaccordProfile) Source() audit.Source { return audit.SourceCanaccordCSV }

func (p *canaccordProfile) Parse(r []string) (*ParsedRow, error) {
	if len(r) < 8 {
		return nil, fmt.Errorf("expected ≥8 columns, got %d", len(r))
	}

	tradeDate, err := time.Parse(canaccordDateFmt, r[0])
	if err != nil {
		return nil, fmt.Errorf("trade date %q: %w", r[0], err)
	}
	settledDate, err := time.Parse(canaccordDateFmt, r[3])
	if err != nil {
		settledDate = tradeDate
	}

	csvType := strings.TrimSpace(r[1])
	accountNo := strings.TrimSpace(r[7])

	status, flagReason, txType := canaccordClassify(csvType)
	if status != RowImport {
		return &ParsedRow{
			Status:       status,
			FlagReason:   flagReason,
			TradeDate:    tradeDate,
			AccountNo:    accountNo,
			SecurityName: extractSecurityName(r[2]),
		}, nil
	}

	qty := decimal.Zero
	if s := strings.TrimSpace(r[4]); s != "" {
		if qty, err = decimal.NewFromString(s); err != nil {
			return nil, fmt.Errorf("quantity %q: %w", s, err)
		}
	}
	if qty.IsNegative() {
		qty = qty.Neg()
	}

	price := decimal.Zero
	if s := strings.TrimSpace(r[5]); s != "" {
		if price, err = decimal.NewFromString(s); err != nil {
			return nil, fmt.Errorf("price %q: %w", s, err)
		}
	}

	amount := decimal.Zero
	if s := strings.TrimSpace(r[6]); s != "" {
		if amount, err = decimal.NewFromString(s); err != nil {
			return nil, fmt.Errorf("amount %q: %w", s, err)
		}
	}

	// commission derivation:
	//   buy:  paid = gross + commission  →  commission = |amount| − gross
	//   sell: received = gross − commission  →  commission = gross − |amount|
	// clamp to zero for rounding diffs
	commission := decimal.Zero
	if price.IsPositive() && qty.IsPositive() {
		gross := qty.Mul(price)
		var diff decimal.Decimal
		if txType == transaction.TypeBuy {
			diff = amount.Abs().Sub(gross)
		} else {
			diff = gross.Sub(amount.Abs())
		}
		if diff.IsPositive() {
			commission = diff
		}
	}

	// for dividends with no per-share price, derive it from amount / qty
	if txType == transaction.TypeDividend && price.IsZero() && qty.IsPositive() {
		price = amount.Abs().Div(qty)
	}

	return &ParsedRow{
		Status:       RowImport,
		TxType:       txType,
		TradeDate:    tradeDate,
		SettledDate:  settledDate,
		SecurityName: extractSecurityName(r[2]),
		AccountNo:    accountNo,
		Quantity:     qty,
		Price:        price,
		Commission:   commission,
	}, nil
}

func canaccordClassify(t string) (status RowStatus, flagReason string, txType transaction.Type) {
	switch t {
	case "Buy", "Pre-Authorized Purchase":
		return RowImport, "", transaction.TypeBuy
	case "Sell":
		return RowImport, "", transaction.TypeSell
	case "Dividend", "Distribution", "Mutual Fund Dividend", "US REIT/MF Distribution", "Interest":
		return RowImport, "", transaction.TypeDividend
	case "Fund Notional Distribution", "Fund Return of Capital",
		"Fee-Based Plan Fee", "Fee-Based Account Fee", "GST", "US Withholding Tax":
		// ROC rows in this export always have $0 amount — real ROC data comes from T3
		return RowSkip, "", ""
	case "Exchange":
		return RowFlag, "fund exchange — record manually", ""
	case "TFSA Contribution", "RRSP Contribution", "Spousal Contribution":
		return RowFlag, "registered account transfer — record manually", ""
	default:
		return RowFlag, fmt.Sprintf("unrecognized type %q", t), ""
	}
}

func extractSecurityName(desc string) string {
	desc = strings.TrimSpace(desc)
	desc = trdSuffix.ReplaceAllString(desc, "")
	for _, marker := range []string{" GRS:", " ECH ", " TFSA "} {
		if i := strings.Index(desc, marker); i >= 0 {
			desc = desc[:i]
		}
	}
	return strings.TrimSpace(desc)
}
