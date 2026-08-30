package yellowcard

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Provider struct {
	apiKey     string
	webhookKey string
	client     *http.Client
	baseURL    string
	sandbox    bool
}

type ycError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ycErrorResponse struct {
	Errors []ycError `json:"errors"`
}

func NewProvider(apiKey, webhookKey string, sandbox bool) *Provider {
	baseURL := "https://api.yellowcard.io"
	if sandbox {
		baseURL = "https://sandbox-api.yellowcard.io"
	}
	return &Provider{
		apiKey:     apiKey,
		webhookKey: webhookKey,
		client:     &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		sandbox:    sandbox,
	}
}

func (p *Provider) Name() string {
	return "yellowcard"
}

func (p *Provider) SupportedCountries() []string {
	return []string{"NG", "GH", "KE", "UG", "TZ", "ZA", "ZM"}
}

func (p *Provider) GetQuote(ctx context.Context, req fiat.QuoteRequest) (*fiat.FiatQuote, error) {
	var ycReq struct {
		SourceCurrency      string `json:"source_currency"`
		DestinationCurrency string `json:"destination_currency"`
		Amount              string `json:"amount"`
		Side                string `json:"side"`
		Country             string `json:"country,omitempty"`
	}

	ycReq.SourceCurrency = req.FiatCurrency
	ycReq.DestinationCurrency = "USDC"
	ycReq.Amount = req.FiatAmount.StringFixed(2)
	ycReq.Side = "sell"
	if req.Country != "" {
		ycReq.Country = req.Country
	}

	body, _ := json.Marshal(ycReq)
	resp, err := p.doRequest(ctx, http.MethodPost, "/v1/rates", body)
	if err != nil {
		return nil, fmt.Errorf("yellowcard get quote: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		apiErr := p.parseError(resp)
		return nil, fmt.Errorf("yellowcard rate error (status %d): %s", resp.StatusCode, apiErr)
	}

	var result struct {
		Rate struct {
			ID                string `json:"id"`
			SourceAmount      string `json:"source_amount"`
			DestinationAmount string `json:"destination_amount"`
			Rate              string `json:"rate"`
			Fee               string `json:"fee"`
			ExpiresAt         string `json:"expires_at"`
			Side              string `json:"side"`
			SourceCurrency    string `json:"source_currency"`
			DestCurrency      string `json:"destination_currency"`
		} `json:"rate"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse yellowcard rate response: %w", err)
	}

	rate, _ := decimal.NewFromString(result.Rate.Rate)
	fee, _ := decimal.NewFromString(result.Rate.Fee)
	srcAmt, _ := decimal.NewFromString(result.Rate.SourceAmount)
	dstAmt, _ := decimal.NewFromString(result.Rate.DestinationAmount)

	expiresAt := time.Now().Add(30 * time.Second)
	if result.Rate.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, result.Rate.ExpiresAt); err == nil {
			expiresAt = t
		}
	}

	return &fiat.FiatQuote{
		Provider:     "yellowcard",
		FiatAmount:   srcAmt,
		FiatCurrency: req.FiatCurrency,
		USDCAmount:   dstAmt,
		Rate:         rate,
		Fee:          fee,
		ExpiresAt:    expiresAt,
	}, nil
}

func (p *Provider) InitiateDeposit(ctx context.Context, req fiat.DepositRequest) (*fiat.DepositInstruction, error) {
	ref := req.Reference
	if ref == "" {
		ref = "DEP-" + uuid.New().String()
	}

	var ycReq struct {
		Amount              string `json:"amount"`
		SourceCurrency      string `json:"source_currency"`
		DestinationCurrency string `json:"destination_currency"`
		CustomerReference   string `json:"customer_reference"`
		Country             string `json:"country,omitempty"`
		Email               string `json:"email,omitempty"`
		CustomerName        string `json:"customer_name,omitempty"`
	}

	ycReq.Amount = req.FiatAmount.StringFixed(2)
	ycReq.SourceCurrency = req.FiatCurrency
	ycReq.DestinationCurrency = "USDC"
	ycReq.CustomerReference = ref
	ycReq.Email = req.CustomerEmail
	ycReq.CustomerName = req.CustomerName

	body, _ := json.Marshal(ycReq)
	resp, err := p.doRequest(ctx, http.MethodPost, "/v1/payments", body)
	if err != nil {
		return nil, fmt.Errorf("yellowcard initiate deposit: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		apiErr := p.parseError(resp)
		return nil, fmt.Errorf("yellowcard deposit error (status %d): %s", resp.StatusCode, apiErr)
	}

	var result struct {
		Payment struct {
			ID           string `json:"id"`
			Reference    string `json:"reference"`
			Status       string `json:"status"`
			Instructions struct {
				BankName      string `json:"bank_name"`
				AccountNumber string `json:"account_number"`
				AccountName   string `json:"account_name"`
				SortCode      string `json:"sort_code"`
				Iban          string `json:"iban"`
				MobileMoney   string `json:"mobile_money"`
				Network       string `json:"network"`
			} `json:"instructions"`
			PaymentLink string `json:"payment_link"`
		} `json:"payment"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse yellowcard payment response: %w", err)
	}

	instructions := make(map[string]string)
	inst := result.Payment.Instructions
	if inst.BankName != "" {
		instructions["bank_name"] = inst.BankName
	}
	if inst.AccountNumber != "" {
		instructions["account_number"] = inst.AccountNumber
	}
	if inst.AccountName != "" {
		instructions["account_name"] = inst.AccountName
	}
	if inst.SortCode != "" {
		instructions["sort_code"] = inst.SortCode
	}
	if inst.Iban != "" {
		instructions["iban"] = inst.Iban
	}
	if inst.MobileMoney != "" {
		instructions["mobile_money"] = inst.MobileMoney
	}
	if inst.Network != "" {
		instructions["network"] = inst.Network
	}
	if result.Payment.PaymentLink != "" {
		instructions["payment_link"] = result.Payment.PaymentLink
	}

	return &fiat.DepositInstruction{
		ProviderRef:  result.Payment.ID,
		Instructions: instructions,
	}, nil
}

func (p *Provider) InitiateWithdrawal(ctx context.Context, req fiat.WithdrawalRequest) (*fiat.WithdrawalResult, error) {
	ref := req.ProviderRef
	if ref == "" {
		ref = "WIT-" + uuid.New().String()
	}

	var ycReq struct {
		Amount              string `json:"amount"`
		SourceCurrency      string `json:"source_currency"`
		DestinationCurrency string `json:"destination_currency"`
		CustomerReference   string `json:"customer_reference"`
		PayoutMethod        string `json:"payout_method"`
		PayoutDetails       struct {
			BankCode      string `json:"bank_code,omitempty"`
			AccountNumber string `json:"account_number,omitempty"`
			MobileMoney   string `json:"mobile_money,omitempty"`
			Network       string `json:"network,omitempty"`
			Country       string `json:"country,omitempty"`
		} `json:"payout_details"`
		Email string `json:"email,omitempty"`
	}

	ycReq.Amount = req.FiatAmount.StringFixed(2)
	ycReq.SourceCurrency = "USDC"
	ycReq.DestinationCurrency = req.FiatCurrency
	ycReq.CustomerReference = ref

	if req.AccountBank != "" && req.AccountNumber != "" {
		ycReq.PayoutMethod = "bank"
		ycReq.PayoutDetails.BankCode = req.AccountBank
		ycReq.PayoutDetails.AccountNumber = req.AccountNumber
	} else {
		ycReq.PayoutMethod = "mobile_money"
		ycReq.PayoutDetails.MobileMoney = req.AccountNumber
		ycReq.PayoutDetails.Network = req.AccountBank
	}
	ycReq.PayoutDetails.Country = req.Country

	body, _ := json.Marshal(ycReq)
	resp, err := p.doRequest(ctx, http.MethodPost, "/v1/payouts", body)
	if err != nil {
		return nil, fmt.Errorf("yellowcard initiate withdrawal: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		apiErr := p.parseError(resp)
		return nil, fmt.Errorf("yellowcard payout error (status %d): %s", resp.StatusCode, apiErr)
	}

	var result struct {
		Payout struct {
			ID        string `json:"id"`
			Reference string `json:"reference"`
			Status    string `json:"status"`
		} `json:"payout"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse yellowcard payout response: %w", err)
	}

	status := "pending"
	switch result.Payout.Status {
	case "completed", "sent", "successful":
		status = "completed"
	case "failed", "cancelled", "reversed":
		status = "failed"
	}

	return &fiat.WithdrawalResult{
		ProviderRef: result.Payout.ID,
		Status:      status,
	}, nil
}

func (p *Provider) GetStatus(ctx context.Context, providerRef string) (*fiat.RailEvent, error) {
	resp, err := p.doRequest(ctx, http.MethodGet, "/v1/transactions/"+providerRef, nil)
	if err != nil {
		return nil, fmt.Errorf("yellowcard get status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		apiErr := p.parseError(resp)
		return nil, fmt.Errorf("yellowcard status error (status %d): %s", resp.StatusCode, apiErr)
	}

	var result struct {
		Transaction struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Status   string `json:"status"`
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"transaction"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse yellowcard status response: %w", err)
	}

	amt, _ := decimal.NewFromString(result.Transaction.Amount)

	evt := &fiat.RailEvent{
		ProviderRef: result.Transaction.ID,
		Status:      "failed",
		Amount:      amt,
		Currency:    result.Transaction.Currency,
	}

	switch result.Transaction.Status {
	case "completed", "sent", "successful":
		evt.Status = "completed"
	case "pending", "processing":
		evt.Status = "pending"
	}

	switch result.Transaction.Type {
	case "payment", "deposit":
		if evt.Status == "completed" {
			evt.Type = fiat.EventDepositConfirmed
		} else {
			evt.Type = fiat.EventDepositFailed
		}
	case "payout", "withdrawal":
		if evt.Status == "completed" {
			evt.Type = fiat.EventWithdrawalSent
		} else {
			evt.Type = fiat.EventWithdrawalFailed
		}
	}

	return evt, nil
}

func (p *Provider) HandleWebhook(ctx context.Context, payload []byte, headers http.Header) (*fiat.RailEvent, error) {
	signature := headers.Get("x-yellowcard-signature")
	if p.webhookKey != "" && signature != "" {
		mac := hmac.New(sha256.New, []byte(p.webhookKey))
		mac.Write(payload)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(signature), []byte(expected)) {
			return nil, fmt.Errorf("invalid webhook signature")
		}
	}

	var data struct {
		Event string `json:"event"`
		Data  struct {
			ID        string `json:"id"`
			Reference string `json:"reference"`
			Type      string `json:"type"`
			Status    string `json:"status"`
			Amount    string `json:"amount"`
			Currency  string `json:"currency"`
		} `json:"data"`
	}

	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, fmt.Errorf("parse yellowcard webhook payload: %w", err)
	}

	amt, _ := decimal.NewFromString(data.Data.Amount)

	evt := &fiat.RailEvent{
		Type:        data.Event,
		ProviderRef: data.Data.ID,
		Amount:      amt,
		Currency:    data.Data.Currency,
		Status:      "failed",
	}

	switch data.Data.Status {
	case "completed", "sent", "successful":
		evt.Status = "completed"
	}

	ref := data.Data.Reference
	if ref != "" {
		evt.ProviderRef = ref
	}

	switch {
	case strings.Contains(data.Event, "payment.completed"), strings.Contains(data.Event, "deposit.completed"):
		evt.Type = fiat.EventDepositConfirmed
	case strings.Contains(data.Event, "payment.failed"), strings.Contains(data.Event, "deposit.failed"):
		evt.Type = fiat.EventDepositFailed
	case strings.Contains(data.Event, "payout.completed"), strings.Contains(data.Event, "withdrawal.completed"):
		evt.Type = fiat.EventWithdrawalSent
	case strings.Contains(data.Event, "payout.failed"), strings.Contains(data.Event, "withdrawal.failed"):
		evt.Type = fiat.EventWithdrawalFailed
	}

	return evt, nil
}

func (p *Provider) doRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	url := p.baseURL + path
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("User-Agent", "Fluxa/1.0")

	return p.client.Do(req)
}

func (p *Provider) parseError(resp *http.Response) string {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("failed to read error body: %v", err)
	}

	var errResp ycErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && len(errResp.Errors) > 0 {
		msgs := make([]string, len(errResp.Errors))
		for i, e := range errResp.Errors {
			msgs[i] = fmt.Sprintf("%s: %s", e.Code, e.Message)
		}
		return strings.Join(msgs, "; ")
	}

	return string(body)
}
