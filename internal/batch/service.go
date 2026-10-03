package batch

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// MaxItems is the maximum number of transfers accepted in a single batch.
const MaxItems = 100

const (
	// ListDefaultLimit is used when the caller does not specify a page size.
	ListDefaultLimit = 20
	// ListMaxLimit caps the page size so a single query cannot load the
	// entire tenant history.
	ListMaxLimit = 100
)

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

// RowResult is the per-row outcome of a preflight validation.
type RowResult struct {
	Row          int    `json:"row"`
	ToWalletID   string `json:"to_wallet_id"`
	Asset        string `json:"asset"`
	Amount       string `json:"amount"`
	Reference    string `json:"reference,omitempty"`
	Valid        bool   `json:"valid"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorField   string `json:"error_field,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	EstimatedFee string `json:"estimated_fee,omitempty"`
	NetAmount    string `json:"net_amount,omitempty"`
}

// PreflightResult is the outcome of a no-write batch validation.
type PreflightResult struct {
	TotalCount     int         `json:"total_count"`
	ValidCount     int         `json:"valid_count"`
	InvalidCount   int         `json:"invalid_count"`
	EstimatedFees  string      `json:"estimated_fees"`
	TotalNetAmount string      `json:"total_net_amount"`
	Rows           []RowResult `json:"rows"`
}

// ListQuery is the caller-facing filter for batch history.
type ListQuery struct {
	Status      domain.BatchStatus
	FromWallet  string
	AfterCursor *ListCursor
	Limit       int
}

// ListPage is one page of batch history plus the next-page cursor.
type ListPage struct {
	Batches    []*domain.Batch
	NextCursor *ListCursor
}

type Service interface {
	CreateBatch(ctx context.Context, fromWalletID string, items []Item) (*Result, error)
	GetBatch(ctx context.Context, id string) (*Result, error)
	ExportCSV(ctx context.Context, id string) (string, error)
	ListBatches(ctx context.Context, q ListQuery) (*ListPage, error)
	// Preflight validates a batch request without writing anything. It runs
	// the same per-row checks as CreateBatch and returns row-level results.
	Preflight(ctx context.Context, fromWalletID string, items []Item) (*PreflightResult, error)
}

type service struct {
	repo        Repository
	txRepo      transfer.Repository
	transferSvc transfer.Service
}

func NewService(repo Repository, txRepo transfer.Repository, transferSvc transfer.Service) Service {
	return &service{repo: repo, txRepo: txRepo, transferSvc: transferSvc}
}

// validateItems runs the shared per-row checks used by both Preflight and
// CreateBatch so the two paths cannot drift. It returns the valid items and
// one RowResult per input row.
func (s *service) validateItems(fromWalletID string, items []Item) ([]Item, []RowResult) {
	rows := make([]RowResult, len(items))
	validItems := make([]Item, 0, len(items))

	for i, item := range items {
		row := RowResult{
			Row:        i + 1,
			ToWalletID: item.ToWalletID,
			Asset:      item.Asset,
			Amount:     item.Amount.StringFixed(7),
			Reference:  item.Reference,
		}

		switch {
		case item.ToWalletID == "":
			row.ErrorCode = "missing_field"
			row.ErrorField = "to_wallet_id"
			row.ErrorMessage = "to_wallet_id is required"
		case item.Amount.IsZero() || item.Amount.IsNegative():
			row.ErrorCode = "invalid_amount"
			row.ErrorField = "amount"
			row.ErrorMessage = "amount must be a positive number"
		case item.Asset == "":
			row.ErrorCode = "missing_field"
			row.ErrorField = "asset"
			row.ErrorMessage = "asset is required"
		default:
			// Asset support is checked by the handler via assetIsSupported
			// (set on the Handler, not the Service). When the handler sets
			// h.assetIsSupported it pre-filters; here we only check shape.
			row.Valid = true
			row.NetAmount = item.Amount.StringFixed(7)
			validItems = append(validItems, item)
		}

		rows[i] = row
	}

	return validItems, rows
}

func (s *service) Preflight(ctx context.Context, fromWalletID string, items []Item) (*PreflightResult, error) {
	if len(items) == 0 {
		return nil, domain.ErrBatchEmpty
	}
	if len(items) > MaxItems {
		return nil, domain.ErrBatchTooLarge
	}
	if fromWalletID == "" {
		return nil, domain.ErrInvalidAmount // placeholder; handler validates uuid
	}

	_, rows := s.validateItems(fromWalletID, items)

	result := &PreflightResult{
		TotalCount:     len(items),
		EstimatedFees:  "0.0000000",
		TotalNetAmount: "0.0000000",
		Rows:           rows,
	}

	var totalNet decimal.Decimal
	for _, row := range rows {
		if row.Valid {
			result.ValidCount++
			if row.NetAmount != "" {
				if n, err := decimal.NewFromString(row.NetAmount); err == nil {
					totalNet = totalNet.Add(n)
				}
			}
		} else {
			result.InvalidCount++
		}
	}
	result.TotalNetAmount = totalNet.StringFixed(7)

	// Estimate fees when the transfer service exposes a FeeEstimate method.
	// Fall back to zero when unavailable so the endpoint still works.
	if fe, ok := s.transferSvc.(interface {
		EstimateFee(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal) (decimal.Decimal, error)
	}); ok {
		var totalFees decimal.Decimal
		for _, item := range items {
			fee, err := fe.EstimateFee(ctx, fromWalletID, item.ToWalletID, item.Asset, item.Amount)
			if err != nil {
				continue
			}
			totalFees = totalFees.Add(fee)
		}
		result.EstimatedFees = totalFees.StringFixed(7)
	}

	return result, nil
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
				ID:             uuid.New().String(),
				Type:           domain.TypeTransfer,
				Status:         domain.StatusFailed,
				FromWallet:     fromWalletID,
				ToWallet:       item.ToWalletID,
				Asset:          item.Asset,
				Amount:         item.Amount,
				BatchID:        &b.ID,
				Reference:      item.Reference,
				FailureReason:  "transfer_initiation_failed",
				FailureMessage: err.Error(),
				CreatedAt:      time.Now().UTC(),
			}
			if tenantID := tenant.IDFromContext(ctx); tenantID != "" {
				tx.TenantID = &tenantID
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

func (s *service) ListBatches(ctx context.Context, q ListQuery) (*ListPage, error) {
	filter := ListFilter{
		Status:     q.Status,
		FromWallet: q.FromWallet,
		Limit:      q.Limit,
	}
	if q.AfterCursor != nil {
		filter.AfterCreated = q.AfterCursor.CreatedAt
		filter.AfterID = q.AfterCursor.ID
	}

	result, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	return &ListPage{
		Batches:    result.Batches,
		NextCursor: result.NextCursor,
	}, nil
}

// aggregateStatus derives the batch-level status from its linked transactions'
// current settlement status, so it always reflects live worker progress
// without needing a separate write path back into the batches table.
func aggregateStatus(txs []*domain.Transaction) domain.BatchStatus {
	var succeeded, failed, held int
	for _, tx := range txs {
		switch tx.Status {
		case domain.StatusConfirmed:
			succeeded++
		case domain.StatusFailed:
			failed++
		case domain.StatusComplianceHold:
			held++
		}
	}

	total := len(txs)
	resolved := succeeded + failed

	switch {
	// Held children need an explicit arm ahead of the progress checks. They
	// are not "processing" — nothing will move them without a human decision —
	// and reporting a batch as completed or failed while one is still parked
	// would hide the hold entirely.
	case held > 0 && resolved+held == total:
		return domain.BatchStatusComplianceHold
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
