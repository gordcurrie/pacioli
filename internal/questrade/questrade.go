package questrade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const oauthEndpoint = "https://login.questrade.com/oauth2/token"

// Secret is an opaque string that redacts itself in logs, fmt output, and JSON
// marshaling. Use Reveal() only at trust boundaries (HTTP headers, SQL params).
type Secret string

func (s Secret) String() string                 { return "[REDACTED]" }
func (s Secret) GoString() string               { return "[REDACTED]" }
func (s Secret) MarshalJSON() ([]byte, error)   { return []byte(`"[REDACTED]"`), nil }
func (s Secret) MarshalText() ([]byte, error)   { return []byte("[REDACTED]"), nil }
func (s Secret) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
func (s Secret) Reveal() string                 { return string(s) }

// Token holds Questrade OAuth2 credentials.
type Token struct {
	AccessToken  Secret    `json:"-"`
	RefreshToken Secret    `json:"-"`
	APIServer    string
	ExpiresAt    time.Time
}

// IsExpired returns true if the access token expires within 60 seconds.
func (t Token) IsExpired() bool {
	return time.Now().Add(60 * time.Second).After(t.ExpiresAt)
}

// Store persists Questrade tokens per user.
type Store interface {
	Save(ctx context.Context, userID int64, token Token) error
	Get(ctx context.Context, userID int64) (Token, error)
	Delete(ctx context.Context, userID int64) error
}

// Exchange converts a refresh token into a new Token. The original refresh
// token is consumed and must not be reused after a successful call.
func Exchange(ctx context.Context, refreshToken string) (Token, error) {
	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthEndpoint,
		strings.NewReader(body.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("questrade exchange: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Do(req) // #nosec G704 -- URL is the oauthEndpoint constant
	if err != nil {
		return Token{}, fmt.Errorf("questrade exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return Token{}, fmt.Errorf("questrade exchange: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	var tr struct {
		AccessToken  string `json:"access_token"`  //nolint:gosec // G117: local parse struct for Questrade token response
		RefreshToken string `json:"refresh_token"` //nolint:gosec // G117
		APIServer    string `json:"api_server"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return Token{}, fmt.Errorf("questrade exchange: decode: %w", err)
	}
	return Token{
		AccessToken:  Secret(tr.AccessToken),
		RefreshToken: Secret(tr.RefreshToken),
		APIServer:    tr.APIServer,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}

// Client calls the Questrade REST API.
type Client struct {
	hc    *http.Client
	token Token
}

// New creates a Client using an already-valid Token.
func New(token Token) *Client {
	return &Client{
		hc:    &http.Client{Timeout: 30 * time.Second},
		token: token,
	}
}

type accountsJSON struct {
	Accounts []struct {
		Number string `json:"number"`
		Type   string `json:"type"`
		Status string `json:"status"`
	} `json:"accounts"`
}

// Account is a Questrade brokerage account summary.
type Account struct {
	Number string
	Type   string
	Status string
}

// Accounts returns all accounts for the authenticated user.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var body accountsJSON
	if err := c.get(ctx, "v1/accounts", &body); err != nil {
		return nil, fmt.Errorf("questrade accounts: %w", err)
	}
	out := make([]Account, len(body.Accounts))
	for i, a := range body.Accounts {
		out[i] = Account{Number: a.Number, Type: a.Type, Status: a.Status}
	}
	return out, nil
}

// Activity is one entry from the Questrade activities endpoint.
type Activity struct {
	TradeDate   time.Time
	SettledDate time.Time
	Action      string
	Symbol      string
	Currency    string
	Quantity    decimal.Decimal
	Price       decimal.Decimal
	GrossAmount decimal.Decimal
	Commission  decimal.Decimal
	NetAmount   decimal.Decimal
	Type        string
}

type activitiesJSON struct {
	Activities []activityJSON `json:"activities"`
}

type activityJSON struct {
	TradeDate   string      `json:"tradeDate"`
	SettledDate string      `json:"settlementDate"`
	Action      string      `json:"action"`
	Symbol      string      `json:"symbol"`
	Currency    string      `json:"currency"`
	Quantity    json.Number `json:"quantity"`
	Price       json.Number `json:"price"`
	GrossAmount json.Number `json:"grossAmount"`
	Commission  json.Number `json:"commission"`
	NetAmount   json.Number `json:"netAmount"`
	Type        string      `json:"type"`
}

// Activities fetches all activities for accountNumber in the half-open interval
// [start, end), automatically splitting into 30-day chunks per API limits.
func (c *Client) Activities(ctx context.Context, accountNumber string, start, end time.Time) ([]Activity, error) {
	var all []Activity
	cur := start
	for cur.Before(end) {
		next := cur.AddDate(0, 0, 30)
		if next.After(end) {
			next = end
		}
		chunk, err := c.fetchActivities(ctx, accountNumber, cur, next)
		if err != nil {
			return nil, err
		}
		all = append(all, chunk...)
		cur = next
	}
	return all, nil
}

func (c *Client) fetchActivities(ctx context.Context, accountNumber string, start, end time.Time) ([]Activity, error) {
	params := url.Values{
		"startTime": {start.UTC().Format(time.RFC3339)},
		"endTime":   {end.UTC().Format(time.RFC3339)},
	}
	path := "v1/accounts/" + url.PathEscape(accountNumber) + "/activities?" + params.Encode()
	var body activitiesJSON
	if err := c.get(ctx, path, &body); err != nil {
		return nil, fmt.Errorf("questrade activities: %w", err)
	}
	acts := make([]Activity, 0, len(body.Activities))
	for i := range body.Activities {
		act, err := parseActivity(&body.Activities[i])
		if err != nil {
			return nil, fmt.Errorf("questrade parse activity: %w", err)
		}
		acts = append(acts, act)
	}
	return acts, nil
}

func parseActivity(a *activityJSON) (Activity, error) {
	tradeDate, err := time.Parse(time.RFC3339Nano, a.TradeDate)
	if err != nil {
		return Activity{}, fmt.Errorf("parse trade date %q: %w", a.TradeDate, err)
	}
	settledDate, err := time.Parse(time.RFC3339Nano, a.SettledDate)
	if err != nil {
		settledDate = tradeDate
	}

	qty, err := numToDecimal(a.Quantity)
	if err != nil {
		return Activity{}, fmt.Errorf("parse quantity: %w", err)
	}
	price, err := numToDecimal(a.Price)
	if err != nil {
		return Activity{}, fmt.Errorf("parse price: %w", err)
	}
	gross, err := numToDecimal(a.GrossAmount)
	if err != nil {
		return Activity{}, fmt.Errorf("parse gross: %w", err)
	}
	comm, err := numToDecimal(a.Commission)
	if err != nil {
		return Activity{}, fmt.Errorf("parse commission: %w", err)
	}
	net, err := numToDecimal(a.NetAmount)
	if err != nil {
		return Activity{}, fmt.Errorf("parse net: %w", err)
	}

	return Activity{
		TradeDate:   tradeDate,
		SettledDate: settledDate,
		Action:      a.Action,
		Symbol:      a.Symbol,
		Currency:    a.Currency,
		Quantity:    qty,
		Price:       price,
		GrossAmount: gross,
		Commission:  comm,
		NetAmount:   net,
		Type:        a.Type,
	}, nil
}

func numToDecimal(n json.Number) (decimal.Decimal, error) {
	s := n.String()
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

func (c *Client) get(ctx context.Context, path string, dest any) error {
	apiServer := strings.TrimSuffix(c.token.APIServer, "/")
	rawURL := apiServer + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token.AccessToken.Reveal())

	resp, err := c.hc.Do(req) // #nosec G704 -- rawURL built from APIServer returned by Questrade OAuth token exchange, not user input
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}
