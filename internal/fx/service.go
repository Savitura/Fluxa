package fx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
)

const (
	quoteTTL        = 30 * time.Second
	quoteKeyPrefix  = "fx:quote:"
	refreshInterval = 30 * time.Second
)

// Quote is a priced, time-limited conversion offer identified by a unique token.
type Quote struct {
	ID         string          `json:"id"`
	OrgID      string          `json:"org_id"`
	FromAsset  string          `json:"from_asset"`
	ToAsset    string          `json:"to_asset"`
	FromAmount decimal.Decimal `json:"from_amount"`
	ToAmount   decimal.Decimal `json:"to_amount"`
	Rate       decimal.Decimal `json:"rate"`
	Fee        decimal.Decimal `json:"fee"`
	ExpiresAt  time.Time       `json:"expires_at"`
	Used       bool            `json:"used"`
}

// FXQuoteAuditRepo persists quote snapshots as an audit trail.
// Redis is the live store; Postgres is the audit log.
type FXQuoteAuditRepo interface {
	CreateQuote(ctx context.Context, q *Quote) error
	MarkQuoteUsed(ctx context.Context, quoteID, conversionID string) error
}

// ConversionRepo persists executed conversions.
type ConversionRepo interface {
	Create(ctx context.Context, c *domain.Conversion) error
}

// Service is the FX domain service interface.
type Service interface {
	GetQuote(ctx context.Context, fromAsset, toAsset, amount string) (*Quote, error)
	ExecuteConversion(ctx context.Context, walletID, quoteID string) (*domain.Conversion, error)
	GetRates(ctx context.Context, from, to string) (*RateResponse, error)
}

type service struct {
	walletRepo     wallet.Repository
	conversionRepo ConversionRepo
	auditRepo      FXQuoteAuditRepo
	feeSvc         fees.Service
	stellar        stellar.Client
	redis          *redis.Client
	rateCache      *RateCache
	usdcIssuer     string
	providers      []Provider
	spreadBps      int

	activePairsMu sync.RWMutex
	activePairs   map[string]struct{}
}

// markUsedScript atomically checks and marks a quote as used.
// Returns the original quote JSON on success, or a Redis error on failure.
var markUsedScript = redis.NewScript(`
local data = redis.call('GET', KEYS[1])
if not data then return redis.error_reply('QUOTE_EXPIRED') end
local q = cjson.decode(data)
if q.used then return redis.error_reply('QUOTE_ALREADY_USED') end
q.used = true
redis.call('SET', KEYS[1], cjson.encode(q), 'KEEPTTL')
return data
`)

// NewService constructs the FX service and starts the background rate refresh.
func NewService(
	walletRepo wallet.Repository,
	convRepo ConversionRepo,
	auditRepo FXQuoteAuditRepo,
	feeSvc fees.Service,
	stellarClient stellar.Client,
	redisClient *redis.Client,
	usdcIssuer string,
	providers []Provider,
	spreadBps int,
) Service {
	s := &service{
		walletRepo:     walletRepo,
		conversionRepo: convRepo,
		auditRepo:      auditRepo,
		feeSvc:         feeSvc,
		stellar:        stellarClient,
		redis:          redisClient,
		rateCache:      NewRateCache(redisClient),
		usdcIssuer:     usdcIssuer,
		providers:      providers,
		spreadBps:      spreadBps,
		activePairs:    make(map[string]struct{}),
	}
	go s.backgroundRefresh(context.Background())
	return s
}

// GetQuote prices a conversion, stores the quote in Redis with a 30-second TTL,
// and writes an audit row to Postgres. Returns the quote with its ID token.
func (s *service) GetQuote(ctx context.Context, fromAsset, toAsset, amount string) (*Quote, error) {
	rateResp, err := s.GetRates(ctx, fromAsset, toAsset)
	if err != nil {
		return nil, err
	}

	fromAmt, err := decimal.NewFromString(amount)
	if err != nil || fromAmt.IsZero() {
		return nil, domain.ErrInvalidAsset
	}

	toAmt := fromAmt.Mul(rateResp.Rate)
	tenantID := tenant.IDFromContext(ctx)

	feeAmt := decimal.Zero
	if feeResult, feeErr := s.feeSvc.CalculateConversionFee(ctx, tenantID, fromAsset, fromAmt); feeErr == nil {
		feeAmt = feeResult.FeeAmount
	}

	q := &Quote{
		ID:         uuid.New().String(),
		OrgID:      tenantID,
		FromAsset:  fromAsset,
		ToAsset:    toAsset,
		FromAmount: fromAmt,
		ToAmount:   toAmt,
		Rate:       rateResp.Rate,
		Fee:        feeAmt,
		ExpiresAt:  time.Now().UTC().Add(quoteTTL),
		Used:       false,
	}

	data, err := json.Marshal(q)
	if err != nil {
		return nil, fmt.Errorf("marshal quote: %w", err)
	}
	if err := s.redis.Set(ctx, quoteKeyPrefix+q.ID, data, quoteTTL).Err(); err != nil {
		return nil, fmt.Errorf("store quote: %w", err)
	}

	if s.auditRepo != nil {
		_ = s.auditRepo.CreateQuote(ctx, q)
	}

	return q, nil
}

// ExecuteConversion fetches a quote by ID from Redis, validates it has not expired
// or been used, atomically marks it used, and records the conversion.
func (s *service) ExecuteConversion(ctx context.Context, walletID, quoteID string) (*domain.Conversion, error) {
	if _, err := s.walletRepo.GetByID(ctx, walletID); err != nil {
		return nil, err
	}

	result, err := markUsedScript.Run(ctx, s.redis, []string{quoteKeyPrefix + quoteID}).Result()
	if err != nil {
		switch err.Error() {
		case "QUOTE_EXPIRED":
			return nil, domain.ErrQuoteExpired
		case "QUOTE_ALREADY_USED":
			return nil, domain.ErrQuoteAlreadyUsed
		default:
			return nil, fmt.Errorf("claim quote: %w", err)
		}
	}

	var q Quote
	if err := json.Unmarshal([]byte(result.(string)), &q); err != nil {
		return nil, fmt.Errorf("decode quote: %w", err)
	}

	conv := &domain.Conversion{
		ID:           uuid.New().String(),
		WalletID:     walletID,
		SourceAsset:  q.FromAsset,
		DestAsset:    q.ToAsset,
		SourceAmount: q.FromAmount,
		DestAmount:   q.ToAmount,
		FeeAmount:    q.Fee,
		Rate:         q.Rate,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.conversionRepo.Create(ctx, conv); err != nil {
		return nil, fmt.Errorf("persist conversion: %w", err)
	}

	if s.auditRepo != nil {
		_ = s.auditRepo.MarkQuoteUsed(ctx, q.ID, conv.ID)
	}

	return conv, nil
}

// GetRates returns a rate for the given pair, serving from the Redis cache and
// falling back to a live provider call on a cache miss.
func (s *service) GetRates(ctx context.Context, from, to string) (*RateResponse, error) {
	if resp, ok := s.rateCache.Get(ctx, from, to); ok {
		return resp, nil
	}

	resp, err := s.fetchRate(ctx, from, to)
	if err != nil {
		return nil, err
	}

	s.rateCache.Set(ctx, from, to, resp)
	s.registerActivePair(from + ":" + to)
	return resp, nil
}

func (s *service) fetchRate(ctx context.Context, from, to string) (*RateResponse, error) {
	pairKey := from + "-" + to
	var selected Provider
	for _, p := range s.providers {
		for _, pair := range p.SupportedPairs() {
			if pair == pairKey {
				selected = p
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("no provider for pair %s", pairKey)
	}

	midRate, err := selected.GetRate(ctx, from, to, "1")
	if err != nil {
		return nil, err
	}

	spreadFactor := decimal.NewFromInt(int64(s.spreadBps)).Div(decimal.NewFromInt(10000))
	finalRate := midRate.Mul(decimal.NewFromInt(1).Add(spreadFactor))

	return &RateResponse{
		Rate:          finalRate,
		MidMarketRate: midRate,
		SpreadBps:     s.spreadBps,
		Provider:      fmt.Sprintf("%T", selected),
		CachedAt:      time.Now().UTC(),
		Stale:         false,
	}, nil
}

func (s *service) registerActivePair(pair string) {
	s.activePairsMu.Lock()
	s.activePairs[pair] = struct{}{}
	s.activePairsMu.Unlock()
}

// backgroundRefresh polls all active pairs every 30 seconds and refreshes
// the Redis rate cache with a 60-second TTL.
func (s *service) backgroundRefresh(ctx context.Context) {
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.activePairsMu.RLock()
			pairs := make([]string, 0, len(s.activePairs))
			for pair := range s.activePairs {
				pairs = append(pairs, pair)
			}
			s.activePairsMu.RUnlock()

			for _, pair := range pairs {
				parts := strings.SplitN(pair, ":", 2)
				if len(parts) != 2 {
					continue
				}
				from, to := parts[0], parts[1]
				if resp, err := s.fetchRate(ctx, from, to); err == nil {
					s.rateCache.Set(ctx, from, to, resp)
				}
			}
		}
	}
}
