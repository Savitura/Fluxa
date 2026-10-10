package transfer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/server/idempotency"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	walletpkg "github.com/fluxa/fluxa/internal/wallet"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

var ErrTransferFinal = errors.New("transfer already final")

type TenantGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Tenant, error)
}

// Screener is the narrow view of internal/compliance this service needs.
// It is declared here, and exchanges only domain types, so the transfer
// package does not depend on the compliance package.
type Screener interface {
	ScreenTransfer(ctx context.Context, req domain.ScreeningRequest) (*domain.ScreeningDecision, error)
	RecordHold(ctx context.Context, tx *domain.Transaction, decision *domain.ScreeningDecision) error
}

// BeneficiaryChecker is optional so existing tenants with no configured
// allowlist retain their current transfer behaviour.
type BeneficiaryChecker interface {
	Check(ctx context.Context, account string) (configured, active bool, err error)
}

type ApprovalGate interface {
	Plan(context.Context, string, string, decimal.Decimal) (*domain.TransferApprovalPolicy, error)
	CreateRequest(context.Context, *domain.Transaction, string, *domain.TransferApprovalPolicy) error
}

type AuditEntry struct {
	Actor     string
	Action    string
	Resource  string
	Timestamp time.Time
}

type AuditLogger interface {
	Record(ctx context.Context, entry AuditEntry) error
}

type ReconcileResult struct {
	WalletID string
	Expected decimal.Decimal
	Actual   decimal.Decimal
	Drift    decimal.Decimal
}

type TransferParams struct {
	FromID            string
	ToID              string
	Asset             string
	Amount            decimal.Decimal
	BatchID           string
	Reference         string
	ExternalReference *string
	Tags              []string
	IdempotencyKey    string
}

type Service interface {
	InitiateTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error)
	// InitiateTransferIdempotent behaves like InitiateTransfer, but first
	// checks whether a transaction already exists for (org, idempotencyKey)
	// and returns it unchanged instead of creating a duplicate. It backs the
	// idempotency-key-protected POST /v1/transfers endpoint; callers that
	// don't need key-scoped dedup (scheduled transfers, fiat settlement) keep
	// using InitiateTransfer directly.
	InitiateTransferIdempotent(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, idempotencyKey string) (*domain.Transaction, error)
	InitiateBatchTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error)
	GetTransaction(ctx context.Context, id string) (*domain.Transaction, error)
	ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error)
	// WithScreener enables compliance screening. It is optional so the
	// worker's screener-less wiring still compiles; when unset, transfers
	// are not screened.
}

func ConfigureStellarClient(svc Service, client stellar.Client) Service {
	if configurable, ok := svc.(interface{ WithStellarClient(stellar.Client) Service }); ok {
		return configurable.WithStellarClient(client)
	}
	return svc
}

func ConfigureScreener(svc Service, screener Screener) Service {
	if configurable, ok := svc.(interface{ WithScreener(Screener) Service }); ok {
		return configurable.WithScreener(screener)
	}
	return svc
}

// Queue is the subset of the asynq-backed queue client the transfer service
// uses. Declaring it as an interface lets callers swap in a double and lets
// ReconcileWallet feature-detect optional queue capabilities.
type Queue interface {
	EnqueueTransfer(ctx context.Context, txID string) error
}

type service struct {
	repo           Repository
	walletRepo     walletpkg.Repository
	feeSvc         fees.Service
	queue          Queue
	tenantRepo     TenantGetter
	stellar        stellar.Client
	clientResolver stellar.ClientResolver
	screener       Screener
	audit          AuditLogger
	beneficiaries  BeneficiaryChecker
	approvals      ApprovalGate
}

func NewService(repo Repository, walletRepo walletpkg.Repository, feeSvc fees.Service, q Queue, tenantRepo ...TenantGetter) Service {
	s := &service{repo: repo, walletRepo: walletRepo, feeSvc: feeSvc, queue: q}
	if len(tenantRepo) > 0 {
		s.tenantRepo = tenantRepo[0]
	}
	return s
}

func (s *service) WithStellarClient(stellarClient stellar.Client) Service {
	s.stellar = stellarClient
	return s
}

func (s *service) WithClientResolver(resolver stellar.ClientResolver) Service {
	s.clientResolver = resolver
	return s
}

func (s *service) client(ctx context.Context) stellar.Client {
	if s.clientResolver != nil {
		if resolved := s.clientResolver.ClientForMode(ctx); resolved != nil {
			return resolved
		}
	}
	return s.stellar
}

// ConfigureClientResolver attaches mode-aware Horizon selection without
// expanding the legacy Service interface implemented by downstream fakes.
func ConfigureClientResolver(svc Service, resolver stellar.ClientResolver) Service {
	if configurable, ok := svc.(interface {
		WithClientResolver(stellar.ClientResolver) Service
	}); ok {
		return configurable.WithClientResolver(resolver)
	}
	return svc
}

func ConfigureBeneficiaryChecker(svc Service, checker BeneficiaryChecker) Service {
	if configurable, ok := svc.(interface {
		WithBeneficiaryChecker(BeneficiaryChecker) Service
	}); ok {
		return configurable.WithBeneficiaryChecker(checker)
	}
	return svc
}

func ConfigureApprovalGate(svc Service, gate ApprovalGate) Service {
	if configurable, ok := svc.(interface{ WithApprovalGate(ApprovalGate) Service }); ok {
		return configurable.WithApprovalGate(gate)
	}
	return svc
}

func (s *service) WithApprovalGate(gate ApprovalGate) Service {
	s.approvals = gate
	return s
}

func (s *service) WithBeneficiaryChecker(checker BeneficiaryChecker) Service {
	s.beneficiaries = checker
	return s
}

func (s *service) WithScreener(screener Screener) Service {
	s.screener = screener
	return s
}

func (s *service) WithAuditLogger(audit AuditLogger) Service {
	s.audit = audit
	return s
}

func (s *service) InitiateTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error) {
	return s.initiate(ctx, TransferParams{
		FromID: fromID,
		ToID:   toID,
		Asset:  asset,
		Amount: amount,
	})
}

func (s *service) InitiateTransferIdempotent(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, idempotencyKey string) (*domain.Transaction, error) {
	return s.InitiateTransferExt(ctx, TransferParams{
		FromID:         fromID,
		ToID:           toID,
		Asset:          asset,
		Amount:         amount,
		IdempotencyKey: idempotencyKey,
	})
}

func (s *service) InitiateBatchTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error) {
	return s.initiate(ctx, TransferParams{
		FromID:    fromID,
		ToID:      toID,
		Asset:     asset,
		Amount:    amount,
		BatchID:   batchID,
		Reference: reference,
	})
}

func (s *service) InitiateTransferExt(ctx context.Context, params TransferParams) (*domain.Transaction, error) {
	if params.IdempotencyKey != "" {
		if recordRepo, ok := s.repo.(IdempotencyRecordRepository); ok {
			if recordID := idempotency.RecordIDFromContext(ctx); recordID != "" {
				if existing, err := recordRepo.GetByIdempotencyRecordID(ctx, recordID); err == nil {
					return existing, nil
				} else if !errors.Is(err, domain.ErrTransactionNotFound) {
					return nil, fmt.Errorf("check idempotency record: %w", err)
				}
			}
		}
		if existing, err := s.repo.GetByIdempotencyKey(ctx, tenant.IDFromContext(ctx), params.IdempotencyKey); err == nil {
			return existing, nil
		} else if !errors.Is(err, domain.ErrTransactionNotFound) {
			return nil, fmt.Errorf("check idempotency key: %w", err)
		}
	}
	return s.initiate(ctx, params)
}

func (s *service) initiate(ctx context.Context, params TransferParams) (*domain.Transaction, error) {
	fromID := params.FromID
	toID := params.ToID
	asset := params.Asset
	amount := params.Amount
	batchID := params.BatchID
	reference := params.Reference
	idempotencyKey := params.IdempotencyKey

	if fromID == toID {
		return nil, domain.ErrSelfTransfer
	}

	tenantID := tenant.IDFromContext(ctx)
	mode := tenant.ModeOrDefault(ctx, domain.ModeLive)
	var monthlyLimit int
	var dailyLimit int
	if tenantID != "" && s.tenantRepo != nil {
		t, err := s.tenantRepo.GetByID(ctx, tenantID)
		if err == nil && t != nil {
			monthlyLimit = t.GetTransferLimit()
			dailyLimit = t.GetDailyTransferLimit()
		}
	}

	srcWallet, err := s.walletRepo.GetByID(ctx, fromID)
	if err != nil {
		return nil, fmt.Errorf("source wallet: %w", err)
	}
	dstWallet, err := s.walletRepo.GetByID(ctx, toID)
	if err != nil {
		return nil, fmt.Errorf("destination wallet: %w", err)
	}
	// Fail before persisting/enqueuing a transfer when Horizon can confirm that
	// the recipient account does not exist. A transient Horizon failure is
	// returned as an error rather than being mistaken for an invalid account.
	if client := s.client(ctx); client != nil {
		if _, err := stellar.LoadAccountWithContext(ctx, client, dstWallet.PublicKey); err != nil {
			if stellar.IsNotFound(err) {
				return nil, domain.ErrBeneficiaryAccountNotFound
			}
			return nil, fmt.Errorf("validate destination Stellar account: %w", err)
		}
	}
	if tenantID != "" && s.beneficiaries != nil {
		configured, active, checkErr := s.beneficiaries.Check(ctx, dstWallet.PublicKey)
		if checkErr != nil {
			return nil, fmt.Errorf("check beneficiary: %w", checkErr)
		}
		if configured && !active {
			return nil, domain.ErrBeneficiaryNotAllowed
		}
	}

	// Validate trustline on source wallet for non-XLM assets
	if asset != "XLM" {
		if err := s.validateTrustline(ctx, fromID, srcWallet.PublicKey, asset); err != nil {
			return nil, err
		}
	}

	status := domain.StatusPending
	var decision *domain.ScreeningDecision
	if s.screener != nil {
		decision, err = s.screener.ScreenTransfer(ctx, domain.ScreeningRequest{
			OrgID:         tenantID,
			FromWalletID:  fromID,
			ToWalletID:    toID,
			FromPublicKey: srcWallet.PublicKey,
			ToPublicKey:   dstWallet.PublicKey,
			Asset:         asset,
			Amount:        amount,
		})
		if err != nil || decision == nil {
			decision = &domain.ScreeningDecision{
				Status:     domain.ScreeningHold,
				RulesFired: []string{"screener_error"},
				Reason:     "screening could not be completed",
				RiskScore:  50,
			}
		}

		switch decision.Status {
		case domain.ScreeningBlocked:
			return nil, domain.ErrTransferBlockedSanctions
		case domain.ScreeningHold:
			status = domain.StatusComplianceHold
		}
	}

	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
	}

	feeResult, err := s.feeSvc.CalculateTransferFee(ctx, tenantID, asset, amount)
	if err != nil {
		return nil, fmt.Errorf("calculate transfer fee: %w", err)
	}

	var batchPtr *string
	if batchID != "" {
		batchPtr = &batchID
	}

	tags := params.Tags
	if tags == nil {
		tags = []string{}
	}

	tx := &domain.Transaction{
		ID:                uuid.New().String(),
		Type:              domain.TypeTransfer,
		Status:            status,
		FromWallet:        fromID,
		ToWallet:          toID,
		Asset:             asset,
		Amount:            amount,
		Fee:               feeResult.FeeAmount,
		FeeBps:            feeResult.FeeBps,
		TenantID:          tenantPtr,
		Mode:              mode,
		BatchID:           batchPtr,
		Reference:         reference,
		ExternalReference: params.ExternalReference,
		Tags:              tags,
		CreatedAt:         time.Now().UTC(),
		IdempotencyKey:    idempotencyKey,
	}
	var approvalPolicy *domain.TransferApprovalPolicy
	if status == domain.StatusPending && s.approvals != nil && tenantID != "" {
		approvalPolicy, err = s.approvals.Plan(ctx, tenantID, asset, amount)
		if err != nil {
			return nil, fmt.Errorf("check transfer approval policy: %w", err)
		}
		if approvalPolicy != nil {
			tx.Status = domain.StatusApprovalPending
		}
	}
	if recordID := idempotency.RecordIDFromContext(ctx); recordID != "" && idempotencyKey != "" {
		tx.IdempotencyRecordID = &recordID
	}

	var createErr error
	now := time.Now().UTC()
	if dailyLimit > 0 {
		dailyRepo, ok := s.repo.(interface {
			CreateWithDailyLimit(context.Context, *domain.Transaction, string, time.Time, int) error
		})
		if !ok {
			return nil, errors.New("daily transfer limit enforcement is unavailable")
		}
		createErr = dailyRepo.CreateWithDailyLimit(ctx, tx, tenantID, now, dailyLimit)
	} else if monthlyLimit > 0 {
		createErr = s.repo.CreateWithMonthlyLimit(ctx, tx, tenantID, now.Year(), now.Month(), monthlyLimit)
	} else {
		createErr = s.repo.Create(ctx, tx)
	}
	if createErr != nil {
		if tx.IdempotencyRecordID != nil && errors.Is(createErr, domain.ErrConcurrentUpdate) {
			if recordRepo, ok := s.repo.(IdempotencyRecordRepository); ok {
				if existing, lookupErr := recordRepo.GetByIdempotencyRecordID(ctx, *tx.IdempotencyRecordID); lookupErr == nil {
					return existing, nil
				}
			}
		}
		return nil, createErr
	}

	actor := "system"
	if uID := tenant.UserIDFromContext(ctx); uID != "" {
		actor = uID
	} else if kID := tenant.APIKeyIDFromContext(ctx); kID != "" {
		actor = kID
	}
	if approvalPolicy != nil {
		creatorID := tenant.UserIDFromContext(ctx)
		if err := s.approvals.CreateRequest(ctx, tx, creatorID, approvalPolicy); err != nil {
			_ = s.repo.UpdateStatus(ctx, tx.ID, domain.StatusFailed, "")
			return nil, fmt.Errorf("create transfer approval request: %w", err)
		}
	}
	s.recordAudit(ctx, actor, "transfer.created", tx.ID)

	if tx.Status == domain.StatusComplianceHold {
		if err := s.screener.RecordHold(ctx, tx, decision); err != nil {
			return nil, fmt.Errorf("record compliance hold: %w", err)
		}
		return tx, nil
	}
	if tx.Status == domain.StatusApprovalPending {
		return tx, nil
	}

	if s.queue != nil {
		if err := s.queue.EnqueueTransfer(ctx, tx.ID); err != nil {
			zerolog.Ctx(ctx).Error().Err(err).Str("transaction_id", tx.ID).Msg("transfer: failed to enqueue settlement job")
			return nil, fmt.Errorf("enqueue settlement: %w", err)
		}
	}

	return tx, nil
}

func (s *service) validateTrustline(ctx context.Context, walletID, publicKey, asset string) error {
	hasTrustline := false

	if s.stellar != nil {
		acct, err := stellar.LoadAccountWithContext(ctx, s.client(ctx), publicKey)
		if err != nil {
			if stellar.IsNotFound(err) {
				return domain.NewErrNoTrustline(asset)
			}
		} else {
			for _, b := range acct.Balances {
				if b.Code == asset {
					hasTrustline = true
					break
				}
			}
			if hasTrustline {
				return nil
			}
		}
	}

	cached, err := s.walletRepo.GetBalances(ctx, walletID)
	if err == nil {
		for _, b := range cached {
			if b.AssetCode == asset {
				hasTrustline = true
				break
			}
		}
	}

	if !hasTrustline {
		return domain.NewErrNoTrustline(asset)
	}

	return nil
}

func (s *service) GetTransaction(ctx context.Context, id string) (*domain.Transaction, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *service) CancelTransfer(ctx context.Context, id, actor, idempotencyKey string) (*domain.Transaction, error) {
	tx, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get transaction: %w", err)
	}

	// Only pending or compliance_hold transfers (with no tx_hash) can be cancelled.
	// This is the pre-submission boundary: once the settlement engine has claimed
	// the transaction (status -> submitted) or a tx_hash has been recorded, cancellation
	// is refused.
	if tx.Status != domain.StatusPending && tx.Status != domain.StatusComplianceHold {
		return nil, &domain.ErrTransferNotCancellable{
			Status: string(tx.Status),
			TxHash: tx.TxHash,
		}
	}

	// Attempt to set status='cancelled' via a conditional UPDATE.
	// The WHERE clause makes this a single conditional UPDATE:
	//   UPDATE transactions SET status = 'cancelled' WHERE id = $1
	// AND status IN ('pending','compliance_hold') AND tx_hash IS NULL.
	// Only one caller can win this transition; concurrent callers race on the same row.
	if err := s.repo.UpdateStatus(ctx, id, domain.StatusCancelled, ""); err != nil {
		return nil, fmt.Errorf("cancel transaction: %w", err)
	}

	// Read the updated transaction to determine the outcome.
	tx, err = s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get transaction after cancel: %w", err)
	}

	// If the status is now cancelled, the cancellation succeeded.
	if tx.Status == domain.StatusCancelled {
		// Record audit log entry with the acting principal.
		s.recordAudit(ctx, actor, "transfer.cancel", id)
		// Dispatch webhook event for cancellation.
		s.dispatchCancelWebhook(ctx, id, actor)
		// Return the updated transaction (idempotent: cancelling an already-cancelled
		// transfer succeeds and just returns the current state).
		return tx, nil
	}

	// The UPDATE did not set the status to cancelled (should not happen given the
	// pre-check, but handle it defensively). Return the current state plainly.
	return nil, &domain.ErrTransferNotCancellable{
		Status: string(tx.Status),
		TxHash: tx.TxHash,
	}
}

func (s *service) dispatchCancelWebhook(ctx context.Context, id, actor string) {
	s.recordAudit(ctx, actor, "transfer.cancel", id)
}

func (s *service) ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.ListByWallet(ctx, walletID, limit, offset)
}

func (s *service) ListTransactionsFiltered(ctx context.Context, filter domain.TransactionFilter) ([]*domain.Transaction, error) {
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filterRepo, ok := s.repo.(FilterableRepository); ok {
		return filterRepo.ListWithFilter(ctx, filter)
	}
	if filter.WalletID != "" {
		return s.repo.ListByWallet(ctx, filter.WalletID, filter.Limit, filter.Offset)
	}
	return nil, nil
}

func (s *service) ForceSettleTransfer(ctx context.Context, id, actor string) (*domain.Transaction, error) {
	tx, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get transaction: %w", err)
	}
	if tx.Status == domain.StatusConfirmed || tx.Status == domain.StatusFailed || tx.Status == domain.StatusReconciliationFailed {
		return nil, ErrTransferFinal
	}
	if s.queue == nil {
		return nil, errors.New("settlement queue not configured")
	}
	if err := s.queue.EnqueueTransfer(ctx, tx.ID); err != nil {
		return nil, fmt.Errorf("enqueue force settle: %w", err)
	}
	tx, err = s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get updated transaction: %w", err)
	}
	s.recordAudit(ctx, actor, "transfer.force_settle", id)
	return tx, nil
}

func (s *service) ReconcileWallet(ctx context.Context, walletID, actor string) (*ReconcileResult, error) {
	if _, err := s.walletRepo.GetByID(ctx, walletID); err != nil {
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	if s.queue == nil {
		return nil, errors.New("reconcile queue not configured")
	}
	type reconcileQueue interface {
		EnqueueReconcile(ctx context.Context, walletID string) (*ReconcileResult, error)
	}
	q, ok := s.queue.(reconcileQueue)
	if !ok {
		return nil, errors.New("reconcile queue does not support reconciliation")
	}
	result, err := q.EnqueueReconcile(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("enqueue reconcile: %w", err)
	}
	s.recordAudit(ctx, actor, "wallet.reconcile", walletID)
	return result, nil
}

func (s *service) recordAudit(ctx context.Context, actor, action, resource string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, AuditEntry{
		Actor:     actor,
		Action:    action,
		Resource:  resource,
		Timestamp: time.Now().UTC(),
	})
}
