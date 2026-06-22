package fx

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Quote struct {
	SourceAsset  string          `json:"source_asset"`
	DestAsset    string          `json:"dest_asset"`
	SourceAmount decimal.Decimal `json:"source_amount"`
	DestAmount   decimal.Decimal `json:"dest_amount"`
	FeeAmount    decimal.Decimal `json:"fee_amount"`
	NetAmount    decimal.Decimal `json:"net_amount"`
	FeeBps       int             `json:"fee_bps"`
	Rate         decimal.Decimal `json:"rate"`
	ExpiresAt    time.Time       `json:"expires_at"`
}

type ConversionRepo interface {
	Create(ctx context.Context, c *domain.Conversion) error
}

type Service interface {
	GetQuote(ctx context.Context, sourceAsset, destAsset, sourceAmount string) (*Quote, error)
	ExecuteConversion(ctx context.Context, walletID string, quote *Quote) (*domain.Conversion, error)
}

type service struct {
	walletRepo     wallet.Repository
	conversionRepo ConversionRepo
	feeSvc         fees.Service
	stellar        stellar.Client
	usdcIssuer     string

	providers []Provider
	cache     *Cache
	spreadBps int // basis points, e.g., 50 = 0.5%
}

func NewService(walletRepo wallet.Repository, convRepo ConversionRepo, feeSvc fees.Service, stellarClient stellar.Client, usdcIssuer string, providers []Provider, cache *Cache, spreadBps int) Service {
	return &service{
		walletRepo:     walletRepo,
		conversionRepo: convRepo,
		feeSvc:         feeSvc,
		stellar:        stellarClient,
		usdcIssuer:     usdcIssuer,
		providers:      providers,
		cache:          cache,
		spreadBps:      spreadBps,
	}
}

func (s *service) GetQuote(ctx context.Context, sourceAsset, destAsset, amount string) (*Quote, error) {
	res, err := s.GetRateInfo(ctx, sourceAsset, destAsset, amount)
	if err != nil {
		return nil, err
	}
	quote := &Quote{
		SourceAsset:  sourceAsset,
		DestAsset:    destAsset,
		SourceAmount: res.SourceAmount,
		DestAmount:   res.DestAmount,
		FeeAmount:    res.FeeAmount,
		NetAmount:    res.NetAmount,
		FeeBps:       res.FeeBps,
		Rate:         res.Rate,
		ExpiresAt:    time.Now().UTC().Add(30 * time.Second),
	}
	return quote, nil
}

// GetRateInfo returns detailed FX rate information, applying caching, provider fallback, spread, and stale handling.
func (s *service) GetRateInfo(ctx context.Context, from, to, amount string) (*RateResponse, error) {
	key := fmt.Sprintf("rate:%s:%s:%s", from, to, amount)
	if cached, ok := s.cache.Get(ctx, key); ok {
		if time.Since(cached.CachedAt) < 30*time.Second {
			return cached, nil
		}
	}

	var selected Provider
	for _, p := range s.providers {
		for _, pair := range p.SupportedPairs() {
			if pair == fmt.Sprintf("%s-%s", from, to) {
				selected = p
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("no provider for pair %s-%s", from, to)
	}

	midRate, err := selected.GetRate(ctx, from, to, amount)
	if err != nil {
		if cached, ok := s.cache.Get(ctx, key); ok {
			cached.Stale = true
			return cached, nil
		}
		return nil, err
	}

	spreadFactor := decimal.NewFromInt(int64(s.spreadBps)).Div(decimal.NewFromInt(10000))
	finalRate := midRate.Mul(decimal.NewFromInt(1).Add(spreadFactor))

	resp := &RateResponse{
		Rate:          finalRate,
		MidMarketRate: midRate,
		SpreadBps:     s.spreadBps,
		Provider:      fmt.Sprintf("%T", selected),
		CachedAt:      time.Now().UTC(),
		Stale:         false,
		SourceAmount:  decimal.NewFromInt(0),
		DestAmount:    decimal.NewFromInt(0),
		FeeAmount:     decimal.Zero,
		NetAmount:     decimal.Zero,
		FeeBps:        0,
	}

	destAmt, _ := decimal.NewFromString(amount)
	if !finalRate.IsZero() {
		resp.SourceAmount = destAmt.Div(finalRate)
	}
	resp.DestAmount = destAmt

	tenantID := tenant.IDFromContext(ctx)
	feeResult, feeErr := s.feeSvc.CalculateConversionFee(ctx, tenantID, from, resp.SourceAmount)
	if feeErr == nil {
		resp.FeeAmount = feeResult.FeeAmount
		resp.NetAmount = feeResult.NetAmount
		resp.FeeBps = feeResult.FeeBps
	}

	s.cache.Set(ctx, key, resp)
	return resp, nil
}

func (s *service) ExecuteConversion(ctx context.Context, walletID string, quote *Quote) (*domain.Conversion, error) {
	if time.Now().After(quote.ExpiresAt) {
		return nil, domain.ErrSlippageExceeded
	}

	if _, err := s.walletRepo.GetByID(ctx, walletID); err != nil {
		return nil, err
	}

	conv := &domain.Conversion{
		ID:           uuid.New().String(),
		WalletID:     walletID,
		SourceAsset:  quote.SourceAsset,
		DestAsset:    quote.DestAsset,
		SourceAmount: quote.SourceAmount,
		DestAmount:   quote.DestAmount,
		FeeAmount:    quote.FeeAmount,
		FeeBps:       quote.FeeBps,
		Rate:         quote.Rate,
		CreatedAt:    time.Now().UTC(),
	}

	if err := s.conversionRepo.Create(ctx, conv); err != nil {
		return nil, fmt.Errorf("persist conversion: %w", err)
	}

	return conv, nil
}
