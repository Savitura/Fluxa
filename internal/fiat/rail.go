package fiat

import (
	"context"

	"github.com/shopspring/decimal"
)

type DepositRequest struct {
	WalletID      string
	Reference     string
	FiatAmount    decimal.Decimal
	FiatCurrency  string
	CustomerEmail string
	CustomerName  string
}

type DepositResponse struct {
	PaymentLink string
	Reference   string
}

type WithdrawRequest struct {
	WalletID      string
	Reference     string
	FiatAmount    decimal.Decimal
	FiatCurrency  string
	AccountBank   string
	AccountNumber string
}

type WithdrawResponse struct {
	Reference string
	Status    string
}

type RailEvent struct {
	Type      string // "deposit.successful", "deposit.failed", "withdraw.successful", "withdraw.failed"
	Reference string
	Status    string // "completed", "failed"
}

type Rail interface {
	Deposit(ctx context.Context, req DepositRequest) (*DepositResponse, error)
	Withdraw(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error)
	HandleWebhook(ctx context.Context, payload []byte, signature string) (*RailEvent, error)
}
