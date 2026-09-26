package fiat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fx"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/google/uuid"
)

type Repository interface {
	CreateDeposit(ctx context.Context, d *domain.FiatDeposit) error
	UpdateDepositStatus(ctx context.Context, id, status string) error
	// ClaimDepositForProcessing atomically transitions a deposit from
	// pending to processing, returning an error if it is not currently
	// pending (already claimed by a concurrent/duplicate webhook delivery,
	// or already in a terminal state). Callers must not move funds for a
	// deposit until this succeeds.
	ClaimDepositForProcessing(ctx context.Context, id string) error
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
	// Get live exchange rate from FX service. We fetch the quote for 1 USDC to determine the rate.
	quote, err := s.fxSvc.GetQuote(ctx, "USDC", req.FiatCurrency, "1")
	if err != nil {
		return nil, fmt.Errorf("get FX rate for withdrawal: %w", err)
	}

	rate := quote.Rate
	if rate.IsZero() {
		return nil, fmt.Errorf("FX service returned a zero exchange rate")
	}

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
	_, err = s.transferSvc.InitiateTransfer(ctx, req.WalletID, s.platformWalletID, "USDC", usdcAmount)
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

	if evt.Type == EventDepositConfirmed || evt.Type == EventDepositFailed {
		deposit, err := s.repo.GetDepositByReference(ctx, evt.ProviderRef)
		if err != nil {
			return fmt.Errorf("get deposit by ref: %w", err)
		}

		if deposit.Status != domain.FiatStatusPending {
			return nil // already claimed/processed — duplicate or replayed delivery
		}

		if !evt.Amount.Equal(deposit.FiatAmount) || !strings.EqualFold(evt.Currency, deposit.FiatCurrency) {
			return fmt.Errorf(
				"webhook amount/currency mismatch for deposit %s: event has %s %s, expected %s %s",
				deposit.ID, evt.Amount, evt.Currency, deposit.FiatAmount, deposit.FiatCurrency,
			)
		}

		// Atomically claim the deposit BEFORE moving any funds. This is
		// what makes a concurrent or duplicate webhook delivery for the
		// same event safe: only the caller that wins this pending ->
		// processing transition proceeds to credit the wallet, so the
		// same deposit can never be credited twice.
		if err := s.repo.ClaimDepositForProcessing(ctx, deposit.ID); err != nil {
			return nil // lost the race, or already handled — idempotent no-op
		}

		if evt.Status == "completed" {
			if _, err := s.transferSvc.InitiateTransfer(ctx, s.platformWalletID, deposit.WalletID, "USDC", deposit.USDCAmount); err != nil {
				_ = s.repo.UpdateDepositStatus(ctx, deposit.ID, domain.FiatStatusFailed)
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

	} else if evt.Type == EventWithdrawalSent || evt.Type == EventWithdrawalFailed {
		withdrawal, err := s.repo.GetWithdrawalByReference(ctx, evt.ProviderRef)
		if err != nil {
			return fmt.Errorf("get withdrawal by ref: %w", err)
		}

		if withdrawal.Status != domain.FiatStatusPending {
			return nil
		}

		if evt.Status == "completed" {
			if err := s.repo.UpdateWithdrawalStatus(ctx, withdrawal.ID, domain.FiatStatusCompleted); err != nil {
				if err.Error() == fmt.Sprintf("withdrawal %s already processed or not pending", withdrawal.ID) {
					return nil
				}
				return fmt.Errorf("update withdrawal status: %w", err)
			}
		} else if evt.Status == "failed" {
			if err := s.repo.UpdateWithdrawalStatus(ctx, withdrawal.ID, domain.FiatStatusFailed); err != nil {
				if err.Error() == fmt.Sprintf("withdrawal %s already processed or not pending", withdrawal.ID) {
					return nil
				}
				return fmt.Errorf("update withdrawal status: %w", err)
			}
			// Refund the user for failed withdrawal
			_, refundErr := s.transferSvc.InitiateTransfer(ctx, s.platformWalletID, withdrawal.WalletID, "USDC", withdrawal.USDCAmount)
			if refundErr != nil {
				return fmt.Errorf("refund user wallet for failed withdrawal: %w", refundErr)
			}
		}
	}

	return nil
}
