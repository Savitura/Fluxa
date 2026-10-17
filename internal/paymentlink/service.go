package paymentlink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrNotFound            = errors.New("payment link not found")
	ErrNotAvailable        = errors.New("payment link is expired or unavailable")
	ErrTenantMissing       = errors.New("tenant context is required")
	ErrInvalidRequest      = errors.New("invalid payment link request")
	ErrIdempotencyConflict = errors.New("idempotency key was already used for a different payment link")
)

type Link struct {
	ID             string
	Token          string
	TenantID       string
	WalletID       string
	Mode           domain.Mode
	Amount         decimal.Decimal
	Currency       string
	Status         string
	ExpiresAt      time.Time
	CreatedAt      time.Time
	IdempotencyKey string
}

type CreateParams struct {
	WalletID       string
	Amount         decimal.Decimal
	Currency       string
	ExpiresAt      time.Time
	IdempotencyKey string
}

type Repository interface {
	Create(context.Context, *Link) error
	GetByIdempotencyKey(context.Context, string) (*Link, error)
	List(context.Context) ([]*Link, error)
	Get(context.Context, string) (*Link, error)
	GetPublic(context.Context, string) (*Link, error)
	Claim(context.Context, string) (*Link, error)
	Release(context.Context, string) error
	ReleaseStale(ctx context.Context, claimedBefore time.Time, limit int) (int, error)
	Cancel(context.Context, string) error
}

type Depositor interface {
	InitiateDeposit(context.Context, fiat.DepositRequest) (*fiat.DepositResponse, error)
}

type Service struct {
	repo      Repository
	depositor Depositor
	now       func() time.Time
}

// releaseBatchSize bounds each ReleaseStale statement.
const releaseBatchSize = 500

// DefaultCheckoutTimeout is how long a link may sit in processing with no
// payment seen before ReleaseAbandoned returns it to active.
const DefaultCheckoutTimeout = 30 * time.Minute

func NewService(repo Repository, depositor Depositor) *Service {
	return &Service{repo: repo, depositor: depositor, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*Link, error) {
	tenantID := tenant.IDFromContext(ctx)
	if tenantID == "" {
		return nil, ErrTenantMissing
	}
	mode, ok := tenant.ModeFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("payment link environment mode is required")
	}
	if params.IdempotencyKey == "" || len(params.IdempotencyKey) > 255 {
		return nil, fmt.Errorf("%w: valid idempotency key is required", ErrInvalidRequest)
	}
	if _, err := uuid.Parse(params.IdempotencyKey); err != nil {
		return nil, fmt.Errorf("%w: idempotency key must be a UUID", ErrInvalidRequest)
	}
	currency := strings.ToUpper(strings.TrimSpace(params.Currency))
	if existing, err := s.repo.GetByIdempotencyKey(ctx, params.IdempotencyKey); err == nil {
		if sameRequest(existing, params, currency) {
			return existing, nil
		}
		return nil, ErrIdempotencyConflict
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if params.WalletID == "" || !params.Amount.IsPositive() || params.Amount.Exponent() < -4 {
		return nil, domain.ErrInvalidAmount
	}
	if len(currency) < 3 || len(currency) > 10 {
		return nil, fmt.Errorf("%w: currency must be a valid fiat currency code", ErrInvalidRequest)
	}
	if validator, ok := s.depositor.(interface{ SupportsCurrency(string) bool }); ok && !validator.SupportsCurrency(currency) {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, domain.ErrUnsupportedFiatCurrency)
	}
	now := s.now()
	if !params.ExpiresAt.After(now) || params.ExpiresAt.After(now.Add(90*24*time.Hour)) {
		return nil, fmt.Errorf("%w: expiry must be within the next 90 days", ErrInvalidRequest)
	}
	link := &Link{
		ID:             uuid.NewString(),
		Token:          uuid.NewString(),
		TenantID:       tenantID,
		WalletID:       params.WalletID,
		Mode:           mode,
		Amount:         params.Amount,
		Currency:       currency,
		Status:         "active",
		ExpiresAt:      params.ExpiresAt.UTC(),
		CreatedAt:      now,
		IdempotencyKey: params.IdempotencyKey,
	}
	if err := s.repo.Create(ctx, link); err != nil {
		if errors.Is(err, ErrIdempotencyConflict) {
			if existing, lookupErr := s.repo.GetByIdempotencyKey(ctx, params.IdempotencyKey); lookupErr == nil && sameRequest(existing, params, currency) {
				return existing, nil
			}
		}
		return nil, err
	}
	return link, nil
}

func sameRequest(link *Link, params CreateParams, currency string) bool {
	return link.WalletID == params.WalletID && link.Amount.Equal(params.Amount) &&
		link.Currency == currency && link.ExpiresAt.Equal(params.ExpiresAt.UTC())
}

func (s *Service) List(ctx context.Context) ([]*Link, error) {
	if tenant.IDFromContext(ctx) == "" {
		return nil, ErrTenantMissing
	}
	links, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if link.Status == "active" && !link.ExpiresAt.After(s.now()) {
			link.Status = "expired"
		}
	}
	return links, nil
}

func (s *Service) Get(ctx context.Context, id string) (*Link, error) {
	if tenant.IDFromContext(ctx) == "" {
		return nil, ErrTenantMissing
	}
	link, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if link.Status == "active" && !link.ExpiresAt.After(s.now()) {
		link.Status = "expired"
	}
	return link, nil
}

func (s *Service) Cancel(ctx context.Context, id string) error {
	if tenant.IDFromContext(ctx) == "" {
		return ErrTenantMissing
	}
	return s.repo.Cancel(ctx, id)
}

func (s *Service) GetPublic(ctx context.Context, token string) (*Link, error) {
	link, err := s.repo.GetPublic(ctx, token)
	if err != nil {
		return nil, err
	}
	if link.Status == "active" && !link.ExpiresAt.After(s.now()) {
		link.Status = "expired"
	}
	return link, nil
}

func (s *Service) Checkout(ctx context.Context, token, email, name string) (*fiat.DepositResponse, error) {
	link, err := s.repo.Claim(ctx, token)
	if err != nil {
		return nil, err
	}
	ctx = tenant.WithID(ctx, link.TenantID)
	ctx = tenant.WithMode(ctx, link.Mode)
	resp, err := s.depositor.InitiateDeposit(ctx, fiat.DepositRequest{
		WalletID:      link.WalletID,
		PaymentLinkID: link.ID,
		Reference:     "PL-" + uuid.NewString(),
		FiatAmount:    link.Amount,
		FiatCurrency:  link.Currency,
		CustomerEmail: email,
		CustomerName:  name,
	})
	if err != nil {
		if releaseErr := s.repo.Release(ctx, link.ID); releaseErr != nil {
			return nil, fmt.Errorf("initiate payment and restore link: %v: %w", releaseErr, err)
		}
		return nil, err
	}
	return resp, nil
}

// ReleaseAbandoned returns links stuck in processing for longer than timeout
// to active. Checkout only restores a link when InitiateDeposit itself fails;
// a payer who starts a deposit and never pays would otherwise leave the link
// unusable until it expires.
func (s *Service) ReleaseAbandoned(ctx context.Context, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		timeout = DefaultCheckoutTimeout
	}
	claimedBefore := s.now().Add(-timeout)
	total := 0
	for {
		n, err := s.repo.ReleaseStale(ctx, claimedBefore, releaseBatchSize)
		total += n
		if err != nil {
			return total, err
		}
		if n < releaseBatchSize {
			return total, nil
		}
	}
}
