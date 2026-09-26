package fiat

import (
	"context"
	"fmt"
	"net/http"

	"github.com/shopspring/decimal"
)

type (
	DepositRequest struct {
		WalletID      string
		Reference     string
		FiatAmount    decimal.Decimal
		FiatCurrency  string
		CustomerEmail string
		CustomerName  string
	}

	DepositResponse struct {
		PaymentLink string
		Reference   string
	}

	WithdrawRequest struct {
		WalletID      string
		Reference     string
		FiatAmount    decimal.Decimal
		FiatCurrency  string
		AccountBank   string
		AccountNumber string
	}

	WithdrawResponse struct {
		Reference string
		Status    string
	}
)

type Rail interface {
	Deposit(ctx context.Context, req DepositRequest) (*DepositResponse, error)
	Withdraw(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error)
	HandleWebhook(ctx context.Context, payload []byte, signature string) (*RailEvent, error)
}

type RailAdapter struct {
	provider Provider
}

func NewRailAdapter(p Provider) *RailAdapter {
	return &RailAdapter{provider: p}
}

func (a *RailAdapter) Deposit(ctx context.Context, req DepositRequest) (*DepositResponse, error) {
	inst, err := a.provider.InitiateDeposit(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("initiate deposit: %w", err)
	}
	return &DepositResponse{
		PaymentLink: inst.Instructions["payment_link"],
		Reference:   inst.ProviderRef,
	}, nil
}

func (a *RailAdapter) Withdraw(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error) {
	wReq := WithdrawalRequest{
		WalletID:      req.WalletID,
		ProviderRef:   req.Reference,
		FiatAmount:    req.FiatAmount,
		FiatCurrency:  req.FiatCurrency,
		AccountBank:   req.AccountBank,
		AccountNumber: req.AccountNumber,
	}
	result, err := a.provider.InitiateWithdrawal(ctx, wReq)
	if err != nil {
		return nil, fmt.Errorf("initiate withdrawal: %w", err)
	}
	return &WithdrawResponse{
		Reference: result.ProviderRef,
		Status:    result.Status,
	}, nil
}

func (a *RailAdapter) HandleWebhook(ctx context.Context, payload []byte, signature string) (*RailEvent, error) {
	headers := make(http.Header)
	if signature != "" {
		headers.Set("verif-hash", signature)
	}
	return a.provider.HandleWebhook(ctx, payload, headers)
}
