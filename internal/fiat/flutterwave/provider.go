package flutterwave

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/shopspring/decimal"
)

type Provider struct {
	secretKey   string
	webhookHash string
	client      *http.Client
	baseURL     string
}

func NewProvider(secretKey, webhookHash string) *Provider {
	return &Provider{
		secretKey:   secretKey,
		webhookHash: webhookHash,
		client:      &http.Client{},
		baseURL:     "https://api.flutterwave.com/v3",
	}
}

func (p *Provider) Name() string {
	return "flutterwave"
}

func (p *Provider) SupportedCountries() []string {
	return []string{"NG", "GH", "KE", "ZA", "UG", "TZ"}
}

func (p *Provider) GetQuote(ctx context.Context, req fiat.QuoteRequest) (*fiat.FiatQuote, error) {
	if p.secretKey == "mock" || p.secretKey == "" {
		rate := decimal.NewFromInt(1500)
		usdcAmt := req.FiatAmount.Div(rate)
		return &fiat.FiatQuote{
			Provider:     "flutterwave",
			FiatAmount:   req.FiatAmount,
			FiatCurrency: req.FiatCurrency,
			USDCAmount:   usdcAmt,
			Rate:         rate,
			Fee:          decimal.NewFromInt(0),
			ExpiresAt:    time.Now().Add(30 * time.Second),
		}, nil
	}
	return nil, fmt.Errorf("flutterwave: GetQuote not yet implemented for production")
}

func (p *Provider) InitiateDeposit(ctx context.Context, req fiat.DepositRequest) (*fiat.DepositInstruction, error) {
	if p.secretKey == "mock" || p.secretKey == "" {
		return &fiat.DepositInstruction{
			ProviderRef: req.Reference,
			Instructions: map[string]string{
				"payment_link": fmt.Sprintf("https://mock.flutterwave.com/pay/%s", req.Reference),
			},
		}, nil
	}

	payload := map[string]interface{}{
		"tx_ref":       req.Reference,
		"amount":       req.FiatAmount.String(),
		"currency":     req.FiatCurrency,
		"redirect_url": "https://fluxa.io/payment/callback",
		"customer": map[string]string{
			"email": req.CustomerEmail,
			"name":  req.CustomerName,
		},
	}
	body, _ := json.Marshal(payload)

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/payments", bytes.NewBuffer(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.secretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("flutterwave deposit api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from flutterwave: %d", resp.StatusCode)
	}

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Link string `json:"link"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &fiat.DepositInstruction{
		ProviderRef: req.Reference,
		Instructions: map[string]string{
			"payment_link": result.Data.Link,
		},
	}, nil
}

func (p *Provider) InitiateWithdrawal(ctx context.Context, req fiat.WithdrawalRequest) (*fiat.WithdrawalResult, error) {
	if p.secretKey == "mock" || p.secretKey == "" {
		return &fiat.WithdrawalResult{
			ProviderRef: req.ProviderRef,
			Status:      "pending",
		}, nil
	}

	payload := map[string]interface{}{
		"account_bank":   req.AccountBank,
		"account_number": req.AccountNumber,
		"amount":         req.FiatAmount.String(),
		"currency":       req.FiatCurrency,
		"reference":      req.ProviderRef,
		"narration":      "Fluxa Withdrawal",
	}
	body, _ := json.Marshal(payload)

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/transfers", bytes.NewBuffer(body))
	httpReq.Header.Set("Authorization", "Bearer "+p.secretKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("flutterwave withdraw api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from flutterwave transfer: %d", resp.StatusCode)
	}

	var result struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Status    string `json:"status"`
			Reference string `json:"reference"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &fiat.WithdrawalResult{
		ProviderRef: result.Data.Reference,
		Status:      result.Data.Status,
	}, nil
}

func (p *Provider) GetStatus(ctx context.Context, providerRef string) (*fiat.RailEvent, error) {
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/transfers/"+providerRef, nil)
	httpReq.Header.Set("Authorization", "Bearer "+p.secretKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("flutterwave get status: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Status string `json:"status"`
		Data   struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	evt := &fiat.RailEvent{
		ProviderRef: providerRef,
		Status:      "failed",
	}
	if result.Data.Status == "successful" {
		evt.Status = "completed"
	}
	return evt, nil
}

func (p *Provider) HandleWebhook(ctx context.Context, payload []byte, headers http.Header) (*fiat.RailEvent, error) {
	if err := p.verifyWebhookSignature(headers); err != nil {
		return nil, err
	}

	var data struct {
		Event string `json:"event"`
		Data  struct {
			ID        json.Number     `json:"id"`
			TxRef     string          `json:"tx_ref"`
			Status    string          `json:"status"`
			Amount    json.RawMessage `json:"amount"`
			Reference string          `json:"reference"`
			Currency  string          `json:"currency"`
		} `json:"data"`
	}

	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, fmt.Errorf("parse webhook payload: %w", err)
	}

	reference := data.Data.TxRef
	if reference == "" {
		reference = data.Data.Reference
	}
	if reference == "" {
		return nil, fmt.Errorf("webhook payload is missing a transaction reference")
	}

	status, err := mapFlutterwaveStatus(data.Data.Status)
	if err != nil {
		return nil, err
	}

	amount, err := parseWebhookAmount(data.Data.Amount)
	if err != nil {
		return nil, err
	}

	evt := &fiat.RailEvent{
		ProviderRef: reference,
		EventID:     data.Data.ID.String(),
		Status:      status,
		Amount:      amount,
		Currency:    data.Data.Currency,
	}

	switch data.Event {
	case "charge.completed":
		evt.Type = fiat.EventDepositConfirmed
	case "transfer.completed":
		evt.Type = fiat.EventWithdrawalSent
	default:
		evt.Type = data.Event
	}

	return evt, nil
}

// verifyWebhookSignature implements Flutterwave's documented webhook
// verification: the "verif-hash" header must match the secret hash
// configured for this integration in the Flutterwave dashboard.
// https://developer.flutterwave.com/docs/integration-guides/webhooks
//
// This fails closed: a webhook secret must be configured for signatures to
// be checked at all, and any mismatch (including a missing header) is
// rejected. "mock" is the same dev/test bypass convention used by the rest
// of this provider (see GetQuote, InitiateDeposit, InitiateWithdrawal).
func (p *Provider) verifyWebhookSignature(headers http.Header) error {
	if p.webhookHash == "mock" {
		return nil
	}
	if p.webhookHash == "" {
		return fmt.Errorf("flutterwave webhook secret is not configured")
	}
	signature := headers.Get("verif-hash")
	if signature == "" {
		return fmt.Errorf("missing webhook signature header")
	}
	// Constant-time compare: a naive != leaks how many leading bytes of the
	// secret matched through response-timing, letting an attacker recover
	// it byte by byte.
	if subtle.ConstantTimeCompare([]byte(signature), []byte(p.webhookHash)) != 1 {
		return fmt.Errorf("invalid webhook signature")
	}
	return nil
}

// maxWebhookAmountScale bounds the fractional precision accepted on inbound
// webhook amounts. Fiat rails settle at two decimal places at most, so six
// is generous headroom; anything beyond it is rejected as over-precision
// rather than silently rounded, keeping the exact-match check in the fiat
// service deterministic.
const maxWebhookAmountScale = 6

// parseWebhookAmount decodes a webhook amount without ever routing the value
// through float64: the raw JSON literal is converted with
// decimal.NewFromString, so values like 100.10 stay exactly 100.10 instead
// of becoming 100.099999999999994.
//
// Policy, applied deterministically:
//   - missing (absent or null) → error
//   - malformed (not a JSON number or numeric string) → error
//   - zero or negative → error (a deposit callback must carry a positive amount)
//   - more than maxWebhookAmountScale fractional digits → error
//   - scientific notation (e.g. 1e2) is accepted: it converts exactly.
func parseWebhookAmount(raw json.RawMessage) (decimal.Decimal, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return decimal.Decimal{}, fmt.Errorf("webhook amount is missing")
	}
	// Flutterwave sends amounts as JSON numbers, but accept a JSON string
	// carrying the same decimal literal for robustness.
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		var unquoted string
		if err := json.Unmarshal(raw, &unquoted); err != nil {
			return decimal.Decimal{}, fmt.Errorf("malformed webhook amount: %w", err)
		}
		s = strings.TrimSpace(unquoted)
	}
	amount, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("malformed webhook amount %q", s)
	}
	if amount.IsNegative() || amount.IsZero() {
		return decimal.Decimal{}, fmt.Errorf("webhook amount must be positive, got %s", amount.String())
	}
	if scale := int(-amount.Exponent()); scale > maxWebhookAmountScale {
		return decimal.Decimal{}, fmt.Errorf(
			"webhook amount exceeds maximum precision of %d decimal places: %s",
			maxWebhookAmountScale, amount.String(),
		)
	}
	return amount, nil
}

// mapFlutterwaveStatus translates a Flutterwave charge/transfer status into
// Fluxa's internal completed/failed vocabulary. Anything that isn't a
// documented terminal status (e.g. "pending") is rejected outright, so an
// in-flight transaction can never be mistaken for a finished one.
func mapFlutterwaveStatus(providerStatus string) (string, error) {
	switch providerStatus {
	case "successful":
		return "completed", nil
	case "failed", "cancelled":
		return "failed", nil
	default:
		return "", fmt.Errorf("unsupported or non-final webhook status: %q", providerStatus)
	}
}
