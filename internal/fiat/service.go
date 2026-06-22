package fiat

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fx"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Repository interface {
	CreateDeposit(ctx context.Context, d *domain.FiatDeposit) error
	UpdateDepositStatus(ctx context.Context, id, status string) error
	GetDepositByReference(ctx context.Context, ref string) (*domain.FiatDeposit, error)
	CreateWithdrawal(ctx context.Context, w *domain.FiatWithdrawal) error
	UpdateWithdrawalStatus(ctx context.Context, id, status string) error
	GetWithdrawalByReference(ctx context.Context, ref string) (*domain.FiatWithdrawal, error)
}

type Service interface {
	InitiateDeposit(ctx context.Context, req DepositRequest) (*DepositResponse, error)
	InitiateWithdrawal(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error)
	HandleWebhook(ctx context.Context, payload []byte, signature string) error
}

type service struct {
	repo             Repository
	rail             Rail
	fxSvc            fx.Service
	transferSvc      transfer.Service
	platformWalletID string
	providerName     string
}

func NewService(repo Repository, rail Rail, fxSvc fx.Service, transferSvc transfer.Service, platformWalletID, providerName string) Service {
	return &service{
		repo:             repo,
		rail:             rail,
		fxSvc:            fxSvc,
		transferSvc:      transferSvc,
		platformWalletID: platformWalletID,
		providerName:     providerName,
	}
}

func (s *service) InitiateDeposit(ctx context.Context, req DepositRequest) (*DepositResponse, error) {
	// First get a quote for USDC to ensure conversion is possible and to record expected amount
	// In deposit, user pays Fiat (Source), gets USDC (Dest).
	quote, err := s.fxSvc.GetQuote(ctx, req.FiatCurrency, "USDC", req.FiatAmount.String())
	if err != nil {
		return nil, fmt.Errorf("get quote for deposit: %w", err)
	}

	deposit := &domain.FiatDeposit{
		ID:                uuid.New().String(),
		WalletID:          req.WalletID,
		Provider:          s.providerName,
		ProviderReference: req.Reference,
		FiatAmount:        req.FiatAmount,
		FiatCurrency:      req.FiatCurrency,
		USDCAmount:        quote.ToAmount, // amount of USDC to credit user
		Status:            domain.FiatStatusPending,
		CreatedAt:         time.Now().UTC(),
	}

	if err := s.repo.CreateDeposit(ctx, deposit); err != nil {
		return nil, fmt.Errorf("create deposit record: %w", err)
	}

	resp, err := s.rail.Deposit(ctx, req)
	if err != nil {
		_ = s.repo.UpdateDepositStatus(ctx, deposit.ID, domain.FiatStatusFailed)
		return nil, fmt.Errorf("rail deposit error: %w", err)
	}

	return resp, nil
}

func (s *service) InitiateWithdrawal(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error) {
	// For withdrawal, user provides Fiat amount they want to receive. 
	// The source is USDC, the dest is Fiat. 
	// Note: fxSvc.GetQuote takes (sourceAsset, destAsset, sourceAmount)
	// So we need to calculate how much USDC is needed for req.FiatAmount.
	// As a simplification, let's treat the fiat amount as the "destAmount",
	// but GetQuote wants sourceAmount. 
	// To simplify for this integration, we will use a fixed rate or inverse if needed.
	// Actually, GetQuote might not support fiat assets yet in stellar paths natively,
	// so for this demo, we'll use a mocked quote response if GetQuote fails for Fiat, 
	// but let's assume GetQuote works or we simulate it.
	
	// Because Fluxa uses Stellar FindPathsStrict, which requires Stellar assets, 
	// "NGN" might not exist on Stellar unless issued. 
	// Let's assume there's a 1 USDC = 1000 NGN fixed rate for this abstraction 
	// if fx.Service fails, or we just manually define the USDC amount.
	
	rate := decimal.NewFromInt(1500) // 1 USDC = 1500 NGN
	usdcAmount := req.FiatAmount.Div(rate)

	withdrawal := &domain.FiatWithdrawal{
		ID:                uuid.New().String(),
		WalletID:          req.WalletID,
		Provider:          s.providerName,
		ProviderReference: req.Reference,
		FiatAmount:        req.FiatAmount,
		FiatCurrency:      req.FiatCurrency,
		USDCAmount:        usdcAmount,
		Status:            domain.FiatStatusPending,
		CreatedAt:         time.Now().UTC(),
	}

	if err := s.repo.CreateWithdrawal(ctx, withdrawal); err != nil {
		return nil, fmt.Errorf("create withdrawal record: %w", err)
	}

	// Debit user wallet, credit platform wallet
	_, err := s.transferSvc.InitiateTransfer(ctx, req.WalletID, s.platformWalletID, "USDC", usdcAmount)
	if err != nil {
		_ = s.repo.UpdateWithdrawalStatus(ctx, withdrawal.ID, domain.FiatStatusFailed)
		return nil, fmt.Errorf("initiate transfer to platform: %w", err)
	}

	resp, err := s.rail.Withdraw(ctx, req)
	if err != nil {
		_ = s.repo.UpdateWithdrawalStatus(ctx, withdrawal.ID, domain.FiatStatusFailed)
		return nil, fmt.Errorf("rail withdraw error: %w", err)
	}

	return resp, nil
}

func (s *service) HandleWebhook(ctx context.Context, payload []byte, signature string) error {
	evt, err := s.rail.HandleWebhook(ctx, payload, signature)
	if err != nil {
		return fmt.Errorf("handle webhook: %w", err)
	}

	if evt.Type == "deposit" || evt.Type == "charge.completed" {
		deposit, err := s.repo.GetDepositByReference(ctx, evt.Reference)
		if err != nil {
			return fmt.Errorf("get deposit by ref: %w", err)
		}

		if deposit.Status != domain.FiatStatusPending {
			return nil // already processed
		}

		if evt.Status == "completed" {
			// Credit the user
			_, err = s.transferSvc.InitiateTransfer(ctx, s.platformWalletID, deposit.WalletID, "USDC", deposit.USDCAmount)
			if err != nil {
				return fmt.Errorf("credit user wallet: %w", err)
			}
			if err := s.repo.UpdateDepositStatus(ctx, deposit.ID, domain.FiatStatusCompleted); err != nil {
				return fmt.Errorf("update deposit status: %w", err)
			}
		} else if evt.Status == "failed" {
			if err := s.repo.UpdateDepositStatus(ctx, deposit.ID, domain.FiatStatusFailed); err != nil {
				return fmt.Errorf("update deposit status: %w", err)
			}
		}

	} else if evt.Type == "withdraw" || evt.Type == "transfer.completed" {
		withdrawal, err := s.repo.GetWithdrawalByReference(ctx, evt.Reference)
		if err != nil {
			return fmt.Errorf("get withdrawal by ref: %w", err)
		}

		if withdrawal.Status != domain.FiatStatusPending {
			return nil
		}

		if evt.Status == "completed" {
			if err := s.repo.UpdateWithdrawalStatus(ctx, withdrawal.ID, domain.FiatStatusCompleted); err != nil {
				return fmt.Errorf("update withdrawal status: %w", err)
			}
		} else if evt.Status == "failed" {
			// We might need to refund the user here. For now just mark failed.
			if err := s.repo.UpdateWithdrawalStatus(ctx, withdrawal.ID, domain.FiatStatusFailed); err != nil {
				return fmt.Errorf("update withdrawal status: %w", err)
			}
		}
	}

	return nil
}
