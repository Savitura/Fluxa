package flutterwave

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fluxa/fluxa/internal/fiat"
)

type Provider struct {
	secretKey    string
	webhookHash  string
	client       *http.Client
	baseURL      string
}

func NewProvider(secretKey, webhookHash string) *Provider {
	return &Provider{
		secretKey:   secretKey,
		webhookHash: webhookHash,
		client:      &http.Client{},
		baseURL:     "https://api.flutterwave.com/v3",
	}
}

func (p *Provider) Deposit(ctx context.Context, req fiat.DepositRequest) (*fiat.DepositResponse, error) {
	// If secretKey is mock, return a mock response
	if p.secretKey == "mock" || p.secretKey == "" {
		return &fiat.DepositResponse{
			PaymentLink: fmt.Sprintf("https://mock.flutterwave.com/pay/%s", req.Reference),
			Reference:   req.Reference,
		}, nil
	}

	payload := map[string]interface{}{
		"tx_ref": req.Reference,
		"amount": req.FiatAmount.String(),
		"currency": req.FiatCurrency,
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

	return &fiat.DepositResponse{
		PaymentLink: result.Data.Link,
		Reference:   req.Reference,
	}, nil
}

func (p *Provider) Withdraw(ctx context.Context, req fiat.WithdrawRequest) (*fiat.WithdrawResponse, error) {
	if p.secretKey == "mock" || p.secretKey == "" {
		return &fiat.WithdrawResponse{
			Reference: req.Reference,
			Status:    "pending",
		}, nil
	}

	payload := map[string]interface{}{
		"account_bank": req.AccountBank,
		"account_number": req.AccountNumber,
		"amount": req.FiatAmount.String(),
		"currency": req.FiatCurrency,
		"reference": req.Reference,
		"narration": "Fluxa Withdrawal",
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

	return &fiat.WithdrawResponse{
		Reference: result.Data.Reference,
		Status:    result.Data.Status,
	}, nil
}

func (p *Provider) HandleWebhook(ctx context.Context, payload []byte, signature string) (*fiat.RailEvent, error) {
	// Verify signature
	if p.webhookHash != "" && p.webhookHash != "mock" {
		if signature != p.webhookHash {
			return nil, fmt.Errorf("invalid webhook signature")
		}
	}

	var data struct {
		Event string `json:"event"`
		Data  struct {
			TxRef  string `json:"tx_ref"`
			Status string `json:"status"`
			Amount float64 `json:"amount"`
			Reference string `json:"reference"`
		} `json:"data"`
	}

	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, fmt.Errorf("parse webhook payload: %w", err)
	}

	evt := &fiat.RailEvent{
		Reference: data.Data.TxRef,
		Status:    "failed",
	}
	if evt.Reference == "" {
		evt.Reference = data.Data.Reference // Used in transfers
	}

	if data.Data.Status == "successful" {
		evt.Status = "completed"
	}

	if data.Event == "charge.completed" {
		evt.Type = "deposit"
	} else if data.Event == "transfer.completed" {
		evt.Type = "withdraw"
	} else {
		// Mock testing override logic
		if len(data.Event) > 0 {
			evt.Type = data.Event // e.g. "deposit", "withdraw"
		} else {
			evt.Type = "deposit" // default fallback
		}
	}

	return evt, nil
}
