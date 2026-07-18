package batch

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// MaxItems is the maximum number of transfers accepted in a single batch.
const MaxItems = 100

type Item struct {
	ToWalletID string
	Asset      string
	Amount     decimal.Decimal
	Reference  string
}

type Result struct {
	Batch        *domain.Batch
	Transactions []*domain.Transaction
}

type Service interface {
	CreateBatch(ctx context.Context, fromWalletID string, items []Item) (*Result, error)
	GetBatch(ctx context.Context, id string) (*Result, error)
	ExportCSV(ctx context.Context, id string) (string, error)
}

type service struct {
	repo        Repository
	txRepo      transfer.Repository
	transferSvc transfer.Service
}

func NewService(repo Repository, txRepo transfer.Repository, transferSvc transfer.Service) Service {
	return &service{repo: repo, txRepo: txRepo, transferSvc: transferSvc}
}

func (s *service) CreateBatch(ctx context.Context, fromWalletID string, items []Item) (*Result, error) {
	if len(items) == 0 {
		return nil, domain.ErrBatchEmpty
	}
	if len(items) > MaxItems {
		return nil, domain.ErrBatchTooLarge
	}

	now := time.Now().UTC()
	b := &domain.Batch{
		ID:         uuid.New().String(),
		Status:     domain.BatchStatusPending,
		TotalCount: len(items),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.repo.Create(ctx, b); err != nil {
		return nil, fmt.Errorf("persist batch: %w", err)
	}

	// Each transfer is submitted independently through the normal transfer
	// pipeline (fee calc, persistence, queue) so the settlement worker picks
	// it up like any other transaction. A failure on one item (e.g. an
	// invalid destination wallet) is recorded as a failed transaction linked
	// to the batch rather than aborting the remaining items.
	txs := make([]*domain.Transaction, 0, len(items))
	for _, item := range items {
		tx, err := s.transferSvc.InitiateBatchTransfer(ctx, fromWalletID, item.ToWalletID, item.Asset, item.Amount, b.ID, item.Reference)
		if err != nil {
			tx = &domain.Transaction{
				ID:         uuid.New().String(),
				Type:       domain.TypeTransfer,
				Status:     domain.StatusFailed,
				FromWallet: fromWalletID,
				ToWallet:   item.ToWalletID,
				Asset:      item.Asset,
				Amount:     item.Amount,
				BatchID:    &b.ID,
				Reference:  item.Reference,
				CreatedAt:  time.Now().UTC(),
			}
			if createErr := s.txRepo.Create(ctx, tx); createErr != nil {
				return nil, fmt.Errorf("persist failed batch item: %w", createErr)
			}
		}
		txs = append(txs, tx)
	}

	return &Result{Batch: b, Transactions: txs}, nil
}

func (s *service) GetBatch(ctx context.Context, id string) (*Result, error) {
	b, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	txs, err := s.txRepo.ListByBatch(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list batch transfers: %w", err)
	}
	b.Status = aggregateStatus(txs)

	return &Result{Batch: b, Transactions: txs}, nil
}

func (s *service) ExportCSV(ctx context.Context, id string) (string, error) {
	result, err := s.GetBatch(ctx, id)
	if err != nil {
		return "", err
	}
	return toCSV(result.Transactions), nil
}

// aggregateStatus derives the batch-level status from its linked transactions'
// current settlement status, so it always reflects live worker progress
// without needing a separate write path back into the batches table.
func aggregateStatus(txs []*domain.Transaction) domain.BatchStatus {
	var succeeded, failed int
	for _, tx := range txs {
		switch tx.Status {
		case domain.StatusConfirmed:
			succeeded++
		case domain.StatusFailed:
			failed++
		}
	}

	total := len(txs)
	resolved := succeeded + failed

	switch {
	case resolved == 0:
		return domain.BatchStatusPending
	case resolved < total:
		return domain.BatchStatusProcessing
	case failed == total:
		return domain.BatchStatusFailed
	case succeeded == total:
		return domain.BatchStatusCompleted
	default:
		return domain.BatchStatusPartial
	}
}
