package broker

import (
	"strings"
	"testing"

	"github.com/gordcurrie/pacioli/internal/transaction"
)

func TestCanaccordClassify(t *testing.T) {
	cases := []struct {
		input      string
		wantStatus RowStatus
		wantType   transaction.Type
	}{
		{"Buy", RowImport, transaction.TypeBuy},
		{"Sell", RowImport, transaction.TypeSell},
		{"Pre-Authorized Purchase", RowImport, transaction.TypeBuy},
		{"Dividend", RowImport, transaction.TypeDividend},
		{"Distribution", RowImport, transaction.TypeDividend},
		{"Mutual Fund Dividend", RowImport, transaction.TypeDividend},
		{"US REIT/MF Distribution", RowImport, transaction.TypeDividend},
		{"Interest", RowImport, transaction.TypeDividend},
		{"Fund Return of Capital", RowSkip, ""},
		{"Fund Notional Distribution", RowSkip, ""},
		{"Fee-Based Plan Fee", RowSkip, ""},
		{"Fee-Based Account Fee", RowSkip, ""},
		{"GST", RowSkip, ""},
		{"US Withholding Tax", RowSkip, ""},
		{"Exchange", RowFlag, ""},
		{"TFSA Contribution", RowFlag, ""},
		{"RRSP Contribution", RowFlag, ""},
		{"Spousal Contribution", RowFlag, ""},
		{"Unknown Type XYZ", RowFlag, ""},
	}

	for _, tc := range cases {
		status, _, txType := canaccordClassify(tc.input)
		if status != tc.wantStatus {
			t.Errorf("classify(%q): status = %v, want %v", tc.input, status, tc.wantStatus)
		}
		if txType != tc.wantType {
			t.Errorf("classify(%q): type = %q, want %q", tc.input, txType, tc.wantType)
		}
	}
}

func TestExtractSecurityName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"INVESCO CANADIAN GOVERNMNT ETF TRD #19994", "INVESCO CANADIAN GOVERNMNT ETF"},
		{"TBR FR QD GLB RL ES TR J J -NL TRD #92477", "TBR FR QD GLB RL ES TR J J -NL"},
		{"DYNAMIC GLB EQ PVT PL CL F -NL GRS: 6000.00 NET: 6000.00 PRC: 18.9976", "DYNAMIC GLB EQ PVT PL CL F -NL"},
		{"MINMALL MINI MALL ST TRST -NL ECH ICC600W TO AVE600W", "MINMALL MINI MALL ST TRST -NL"},
		{"TCFMLP TCYT US CAD - I -NL TFSA TST-ACCT-1", "TCFMLP TCYT US CAD - I -NL"},
		{"BROOKFIELD RENEW ENGY LPU", "BROOKFIELD RENEW ENGY LPU"},
		{"  CENOVUS ENERGY INC  ", "CENOVUS ENERGY INC"},
	}

	for _, tc := range cases {
		got := extractSecurityName(tc.input)
		if got != tc.want {
			t.Errorf("extractSecurityName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestCanaccordParseBuy(t *testing.T) {
	p := NewCanaccordProfile()
	record := []string{
		"2/7/2023 12:00:00 AM",         // Date
		"Buy",                           // Type
		"INVESCO CANADN ETF TRD #19994", // Description
		"2/9/2023 12:00:00 AM",          // Settlement
		"1021.00000",                    // Quantity
		"19.70",                         // Price
		"-20113.70",                     // Amount
		"TST001A1",                      // Account
		"Test User A",                   // Client Name
		"TST001",                        // Client ID
	}

	row, err := p.Parse(record)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if row.Status != RowImport {
		t.Errorf("Status = %v, want RowImport", row.Status)
	}
	if row.TxType != transaction.TypeBuy {
		t.Errorf("TxType = %q, want buy", row.TxType)
	}
	if row.Quantity.String() != "1021" {
		t.Errorf("Quantity = %s, want 1021", row.Quantity)
	}
	if row.Price.String() != "19.7" {
		t.Errorf("Price = %s, want 19.7", row.Price)
	}
	if !row.Commission.IsZero() {
		t.Errorf("Commission = %s, want 0 (fee-based account)", row.Commission)
	}
	if row.AccountNo != "TST001A1" {
		t.Errorf("AccountNo = %q, want TST001A1", row.AccountNo)
	}
	if row.SecurityName != "INVESCO CANADN ETF" {
		t.Errorf("SecurityName = %q, want INVESCO CANADN ETF", row.SecurityName)
	}
}

func TestCanaccordParseBuyWithCommission(t *testing.T) {
	p := NewCanaccordProfile()
	// 100 shares @ $10.00 + $5 commission = $1005 total outflow
	record := []string{
		"2/7/2023 12:00:00 AM", "Buy", "SOME EQUITY TRD #999",
		"2/9/2023 12:00:00 AM", "100.00000", "10.00", "-1005.00",
		"TST001A1", "Test User A", "TST001",
	}
	row, err := p.Parse(record)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if row.TxType != transaction.TypeBuy {
		t.Errorf("TxType = %q, want buy", row.TxType)
	}
	// commission = |amount| − gross = 1005 − 1000 = 5
	if row.Commission.String() != "5" {
		t.Errorf("Commission = %s, want 5", row.Commission)
	}
}

func TestCanaccordParseSellWithCommission(t *testing.T) {
	p := NewCanaccordProfile()
	record := []string{
		"2/1/2023 12:00:00 AM",
		"Sell",
		"TBR FR QD GLB RL ES TR J J -NL TRD #92477",
		"2/1/2023 12:00:00 AM",
		"-1800.97430",
		"11.06",
		"19909.77",
		"TST001A1",
		"Test User A",
		"TST001",
	}

	row, err := p.Parse(record)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if row.TxType != transaction.TypeSell {
		t.Errorf("TxType = %q, want sell", row.TxType)
	}
	if row.Quantity.IsNegative() {
		t.Errorf("Quantity should be positive, got %s", row.Quantity)
	}
	// commission = |qty × price| − |amount| ≈ 9.00
	if row.Commission.IsZero() {
		t.Error("expected non-zero commission for sell with spread")
	}
}

func TestCanaccordParseSkipTypes(t *testing.T) {
	p := NewCanaccordProfile()
	skipTypes := []string{"Fund Return of Capital", "Fee-Based Plan Fee", "GST", "US Withholding Tax"}
	for _, typ := range skipTypes {
		record := []string{
			"3/15/2023 12:00:00 AM", typ, "SOME FUND",
			"3/15/2023 12:00:00 AM", "", "", "-54.22", "TST002A1", "Test User B", "TST002",
		}
		row, err := p.Parse(record)
		if err != nil {
			t.Fatalf("Parse(%q): %v", typ, err)
		}
		if row.Status != RowSkip {
			t.Errorf("Parse(%q): Status = %v, want RowSkip", typ, row.Status)
		}
	}
}

func TestParseCSVHeaderSkip(t *testing.T) {
	csv := `"Date","Type","Description","Settlement","Quantity","Price","Amount","Account","Client","ID"
"3/31/2023 12:00:00 AM","GST","GST note","3/31/2023 12:00:00 AM","","","-2.71","TST002A1","Test User B","TST002"
"2/7/2023 12:00:00 AM","Buy","SOME ETF TRD #100","2/9/2023 12:00:00 AM","100.00000","20.00","-2000.00","TST001A1","Test User A","TST001"
`
	rows, err := ParseCSV(strings.NewReader(csv), NewCanaccordProfile())
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Line != 2 {
		t.Errorf("first row Line = %d, want 2", rows[0].Line)
	}
	if rows[0].Status != RowSkip {
		t.Errorf("GST row should be RowSkip, got %v", rows[0].Status)
	}
	if rows[1].Line != 3 {
		t.Errorf("second row Line = %d, want 3", rows[1].Line)
	}
	if rows[1].Status != RowImport {
		t.Errorf("Buy row should be RowImport, got %v", rows[1].Status)
	}
}
