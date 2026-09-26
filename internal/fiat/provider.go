package fiat

import (
	"context"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
)

type (
	QuoteRequest struct {
		FiatCurrency string
		FiatAmount   decimal.Decimal
		Country      string
	}

	FiatQuote struct {
		Provider     string
		FiatAmount   decimal.Decimal
		FiatCurrency string
		USDCAmount   decimal.Decimal
		Rate         decimal.Decimal
		Fee          decimal.Decimal
		ExpiresAt    time.Time
	}

	DepositInstruction struct {
		ProviderRef  string
		Instructions map[string]string
	}

	WithdrawalRequest struct {
		WalletID      string
		ProviderRef   string
		FiatAmount    decimal.Decimal
		FiatCurrency  string
		Country       string
		AccountBank   string
		AccountNumber string
		CustomerEmail string
		CustomerName  string
	}

	WithdrawalResult struct {
		ProviderRef string
		Status      string
	}

	RailEvent struct {
		Type        string
		ProviderRef string
		// EventID is the provider's own unique identifier for this
		// occurrence (e.g. Flutterwave's numeric transaction id). Unlike
		// ProviderRef (the merchant-supplied reference, stable across a
		// retried/replayed delivery of the same event), this identifies
		// the specific delivery/charge.
		EventID  string
		Status   string
		Amount   decimal.Decimal
		Currency string
	}
)

const (
	EventDepositConfirmed = "deposit.confirmed"
	EventDepositFailed    = "deposit.failed"
	EventWithdrawalSent   = "withdrawal.sent"
	EventWithdrawalFailed = "withdrawal.failed"
)

type Provider interface {
	Name() string
	SupportedCountries() []string
	GetQuote(ctx context.Context, req QuoteRequest) (*FiatQuote, error)
	InitiateDeposit(ctx context.Context, req DepositRequest) (*DepositInstruction, error)
	InitiateWithdrawal(ctx context.Context, req WithdrawalRequest) (*WithdrawalResult, error)
	GetStatus(ctx context.Context, providerRef string) (*RailEvent, error)
	HandleWebhook(ctx context.Context, payload []byte, headers http.Header) (*RailEvent, error)
}
