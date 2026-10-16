package batch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// MaxItems is the maximum number of transfers accepted in a single batch.
const MaxItems = 100

// failureReasonBatchItem is stored on a batch item that failed during batch
// creation itself (e.g. a sanctions-blocked recipient), mirroring the reason
// the single-transfer pipeline records for an initiation failure.
const failureReasonBatchItem = "transfer_initiation_failed"

type Item struct {
	ToWalletID string
	Asset      string
	Amount     decimal.Decimal
	Reference  string
}

type Result struct {
	Batch        *domain.BatchTransfer
	Transactions []*domain.Transaction
}

type Service interface {
	CreateBatch(ctx context.Context, fromWalletID string, items []Item) (*Result, error)
	GetBatch(ctx context.Context, id string) (*Result, error)
	ExportCSV(ctx context.Context, id string) (string, error)
}

// WalletLookup is the narrow view of the wallet repository the pre-validation
// pass needs: batch validation only cares whether a wallet exists for the
// caller, not its balances or keys.
type WalletLookup interface {
	GetByID(ctx context.Context, id string) (*domain.Wallet, error)
}

// Queue is the settlement queue view batch creation needs. Enqueue happens
// only after the batch transaction has committed, never inside it.
type Queue interface {
	EnqueueBatchTransfer(ctx context.Context, txID, batchID string) error
}

// TxRunner runs fn inside a single database transaction, passing a context
// that carries that transaction so every repository called from fn joins it.
// Production wires postgres.RunInTx; tests can pass a passthrough.
type TxRunner func(ctx context.Context, fn func(ctx context.Context) error) error

// Option configures optional batch-service dependencies.
type Option func(*service)

// WithWalletLookup enables the pre-validation pass over wallet existence.
func WithWalletLookup(l WalletLookup) Option {
	return func(s *service) { s.wallets = l }
}

// WithQueue enables the post-commit settlement enqueue.
func WithQueue(q Queue) Option {
	return func(s *service) { s.queue = q }
}

// WithTxRunner enables the single-transaction persistence of a batch. It is
// required: without it CreateBatch refuses to run rather than risk writing
// the batch and its transfers through separate connections.
func WithTxRunner(r TxRunner) Option {
	return func(s *service) { s.txRunner = r }
}

type service struct {
	repo        Repository
	txRepo      transfer.Repository
	transferSvc transfer.Service
	wallets     WalletLookup
	queue       Queue
	txRunner    TxRunner
}

func NewService(repo Repository, txRepo transfer.Repository, transferSvc transfer.Service, opts ...Option) Service {
	s := &service{repo: repo, txRepo: txRepo, transferSvc: transferSvc}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// BatchValidationError reports every invalid row found in a batch request.
// Any entry rejects the whole batch: nothing is prepared, written or
// enqueued until every row passes.
type BatchValidationError struct {
	Errors []ValidationError
}

func (e *BatchValidationError) Error() string {
	return fmt.Sprintf("%d row(s) have validation errors", len(e.Errors))
}

// validateItems runs the cheap deterministic checks for the whole request
// before anything is prepared or persisted: source wallet existence, amount
// sign, self-transfers and per-row destination existence.
func (s *service) validateItems(ctx context.Context, fromWalletID string, items []Item) error {
	var errs []ValidationError

	if s.wallets != nil {
		if _, err := s.wallets.GetByID(ctx, fromWalletID); err != nil {
			errs = append(errs, ValidationError{
				Row:    0,
				Field:  "from_wallet_id",
				Value:  fromWalletID,
				Reason: "wallet not found",
			})
		}
	}

	for i, item := range items {
		switch {
		case item.Amount.LessThanOrEqual(decimal.Zero):
			errs = append(errs, ValidationError{
				Row:    i + 1,
				Field:  "amount",
				Value:  item.Amount.String(),
				Reason: "amount must be a positive number",
			})
		case item.ToWalletID == fromWalletID:
			errs = append(errs, ValidationError{
				Row:    i + 1,
				Field:  "to_wallet_id",
				Value:  item.ToWalletID,
				Reason: "source and destination wallet must differ",
			})
		case s.wallets != nil:
			if _, err := s.wallets.GetByID(ctx, item.ToWalletID); err != nil {
				errs = append(errs, ValidationError{
					Row:    i + 1,
					Field:  "to_wallet_id",
					Value:  item.ToWalletID,
					Reason: "wallet not found",
				})
			}
		}
	}

	if len(errs) > 0 {
		return &BatchValidationError{Errors: errs}
	}
	return nil
}

// CreateBatch accepts up to MaxItems transfers and persists them in three
// phases so a failure at any phase leaves no partial state:
//
//  1. validate: cheap per-row checks; any failure rejects the whole batch
//     with a per-item error list and touches nothing.
//  2. prepare: every item's read-only transfer checks (wallets, Horizon,
//     trustline, fees); the first failure rejects the whole batch.
//  3. persist: the batch row and every transaction are written inside ONE
//     database transaction; settlement is enqueued only after it commits.
func (s *service) CreateBatch(ctx context.Context, fromWalletID string, items []Item) (*Result, error) {
	if len(items) == 0 {
		return nil, domain.ErrBatchEmpty
	}
	if len(items) > MaxItems {
		return nil, domain.ErrBatchTooLarge
	}
	if err := s.validateItems(ctx, fromWalletID, items); err != nil {
		return nil, err
	}
	if s.txRunner == nil {
		return nil, errors.New("batch service: transaction runner not configured")
	}

	now := time.Now().UTC()
	b := &domain.BatchTransfer{
		ID:         uuid.New().String(),
		Status:     domain.BatchStatusPending,
		TotalCount: len(items),
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Phase 2 — prepare every item. Nothing is written here, so the first
	// failure can still reject the batch cleanly.
	prepared := make([]*transfer.PreparedTransfer, 0, len(items))
	for _, item := range items {
		p, err := s.transferSvc.PrepareBatchTransfer(ctx, transfer.TransferParams{
			FromID:    fromWalletID,
			ToID:      item.ToWalletID,
			Asset:     item.Asset,
			Amount:    item.Amount,
			BatchID:   b.ID,
			Reference: item.Reference,
		})
		if err != nil {
			return nil, fmt.Errorf("prepare batch transfer to %s: %w", item.ToWalletID, err)
		}
		prepared = append(prepared, p)
	}

	// Phase 3 — one transaction for the batch row plus every transfer row.
	txs := make([]*domain.Transaction, 0, len(prepared))
	if err := s.txRunner(ctx, func(txCtx context.Context) error {
		if err := s.repo.Create(txCtx, b); err != nil {
			return fmt.Errorf("persist batch: %w", err)
		}
		for i, p := range prepared {
			tx, err := s.transferSvc.PersistBatchTransfer(txCtx, p)
			if err != nil {
				if errors.Is(err, domain.ErrTransferBlockedSanctions) {
					// A sanctions-blocked recipient becomes a failed item of
					// this batch instead of rejecting its siblings: the block
					// row written during screening stays tied to a real
					// transaction, and the caller sees which row failed.
					blocked := &domain.Transaction{
						ID:             uuid.New().String(),
						Type:           domain.TypeTransfer,
						Status:         domain.StatusFailed,
						FromWallet:     fromWalletID,
						ToWallet:       items[i].ToWalletID,
						Asset:          items[i].Asset,
						Amount:         items[i].Amount,
						BatchID:        &b.ID,
						Reference:      items[i].Reference,
						FailureReason:  failureReasonBatchItem,
						FailureMessage: err.Error(),
						CreatedAt:      time.Now().UTC(),
					}
					if tenantID := tenant.IDFromContext(txCtx); tenantID != "" {
						blocked.TenantID = &tenantID
					}
					if createErr := s.txRepo.Create(txCtx, blocked); createErr != nil {
						return fmt.Errorf("persist blocked batch item: %w", createErr)
					}
					txs = append(txs, blocked)
					continue
				}
				return err
			}
			txs = append(txs, tx)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// Enqueue only after the commit. A queue failure is logged rather than
	// returned: the batch is durable now, and failing the request would let
	// an idempotent retry create a second batch for transfers that already
	// exist. Force-settle can still pick an unenqueued row up.
	if s.queue != nil {
		for _, tx := range txs {
			if tx.Status != domain.StatusPending {
				continue
			}
			if err := s.queue.EnqueueBatchTransfer(ctx, tx.ID, b.ID); err != nil {
				zerolog.Ctx(ctx).Error().Err(err).
					Str("transaction_id", tx.ID).
					Str("batch_id", b.ID).
					Msg("batch: failed to enqueue settlement job")
			}
		}
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

	status, succeeded, failed, _ := domain.SummarizeBatch(txs)
	if b.Status != status || b.SucceededCount != succeeded || b.FailedCount != failed {
		// Self-heal: a worker may have updated children without reaching its
		// sync (a crash between commit and sync). Persist the recomputation;
		// the response reflects the live children either way.
		if synced, _, syncErr := s.repo.SyncAggregates(ctx, id); syncErr == nil {
			// The synced row may already be ahead of the listing above — keep it.
			return &Result{Batch: synced, Transactions: txs}, nil
		} else if !errors.Is(syncErr, domain.ErrBatchNotFound) {
			zerolog.Ctx(ctx).Warn().Err(syncErr).Str("batch_id", id).Msg("batch: self-heal sync failed")
		}
	}
	b.Status = status
	b.SucceededCount = succeeded
	b.FailedCount = failed

	return &Result{Batch: b, Transactions: txs}, nil
}

func (s *service) ExportCSV(ctx context.Context, id string) (string, error) {
	result, err := s.GetBatch(ctx, id)
	if err != nil {
		return "", err
	}
	return toCSV(result.Transactions), nil
}
