package refund

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/shopspring/decimal"
)

var (
	ErrNotFound            = errors.New("refund not found")
	ErrNotRefundable       = errors.New("transaction is not refundable")
	ErrAmountExceeded      = errors.New("refund amount exceeds the refundable balance")
	ErrIdempotencyConflict = errors.New("idempotency key was already used for a different refund")
)

type Record struct {
	ID                    string
	OriginalTransactionID string
	RefundTransactionID   string
	FromWallet            string
	ToWallet              string
	Asset                 string
	Amount                decimal.Decimal
	Reason                string
	Status                string
	IdempotencyKey        string
}

type Repository interface {
	Reserve(context.Context, string, decimal.Decimal, string, string) (*Record, error)
	LinkTransaction(context.Context, string, string, string) error
	MarkFailed(context.Context, string) error
	Get(context.Context, string) (*Record, error)
	ListByOriginal(context.Context, string) ([]*Record, error)
}

type TransferService interface {
	InitiateTransferExt(context.Context, transfer.TransferParams) (*domain.Transaction, error)
	GetTransaction(context.Context, string) (*domain.Transaction, error)
}

type Service struct {
	repo     Repository
	transfer TransferService
}

func NewService(repo Repository, transferSvc TransferService) *Service {
	return &Service{repo: repo, transfer: transferSvc}
}

func (s *Service) Create(ctx context.Context, originalID string, amount decimal.Decimal, reason, idempotencyKey string) (*Record, error) {
	if tenant.IDFromContext(ctx) == "" {
		return nil, ErrNotFound
	}
	if originalID == "" || !amount.IsPositive() || idempotencyKey == "" {
		return nil, domain.ErrInvalidAmount
	}
	record, err := s.repo.Reserve(ctx, originalID, amount, reason, idempotencyKey)
	if err != nil {
		return nil, err
	}
	tx, err := s.transfer.InitiateTransferExt(ctx, transfer.TransferParams{
		FromID:         record.FromWallet,
		ToID:           record.ToWallet,
		Asset:          record.Asset,
		Amount:         record.Amount,
		Reference:      "refund:" + record.ID,
		IdempotencyKey: "refund:" + record.ID,
	})
	if err != nil {
		if markErr := s.repo.MarkFailed(ctx, record.ID); markErr != nil {
			return nil, fmt.Errorf("create refund transfer and mark refund failed: %v: %w", markErr, err)
		}
		return nil, err
	}
	if err := s.repo.LinkTransaction(ctx, record.ID, tx.ID, string(tx.Status)); err != nil {
		return nil, fmt.Errorf("link refund transfer: %w", err)
	}
	record.RefundTransactionID = tx.ID
	record.Status = refundStatus(tx.Status)
	return record, nil
}

func (s *Service) Get(ctx context.Context, id string) (*Record, error) {
	if tenant.IDFromContext(ctx) == "" {
		return nil, ErrNotFound
	}
	record, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.refreshStatus(ctx, record)
}

func (s *Service) ListByOriginal(ctx context.Context, originalID string) ([]*Record, error) {
	if tenant.IDFromContext(ctx) == "" {
		return nil, ErrNotFound
	}
	records, err := s.repo.ListByOriginal(ctx, originalID)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if _, err := s.refreshStatus(ctx, record); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func (s *Service) refreshStatus(ctx context.Context, record *Record) (*Record, error) {
	if record.RefundTransactionID == "" {
		return record, nil
	}
	tx, err := s.transfer.GetTransaction(ctx, record.RefundTransactionID)
	if err != nil {
		return nil, err
	}
	record.Status = refundStatus(tx.Status)
	return record, nil
}

func refundStatus(status domain.TransactionStatus) string {
	switch status {
	case domain.StatusConfirmed, domain.StatusSettled:
		return "succeeded"
	case domain.StatusFailed, domain.StatusCancelled, domain.StatusReversed:
		return "failed"
	default:
		return "pending"
	}
}
