package fx

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Quote struct {
	SourceAsset  string          `json:"source_asset"`
	DestAsset    string          `json:"dest_asset"`
	SourceAmount decimal.Decimal `json:"source_amount"`
	DestAmount   decimal.Decimal `json:"dest_amount"`
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
	stellar        stellar.Client
	usdcIssuer     string
}

func NewService(walletRepo wallet.Repository, convRepo ConversionRepo, stellarClient stellar.Client, usdcIssuer string) Service {
	return &service{
		walletRepo:     walletRepo,
		conversionRepo: convRepo,
		stellar:        stellarClient,
		usdcIssuer:     usdcIssuer,
	}
}

func (s *service) GetQuote(ctx context.Context, sourceAsset, destAsset, amount string) (*Quote, error) {
	issuer := ""
	if destAsset == "USDC" {
		issuer = s.usdcIssuer
	}

	paths, err := s.stellar.FindPathsStrict("", destAsset, issuer, amount)
	if err != nil {
		return nil, fmt.Errorf("find paths: %w", err)
	}

	if len(paths) == 0 {
		return nil, domain.ErrInvalidAsset
	}

	best := paths[0]
	srcAmt, _ := decimal.NewFromString(best.SourceAmount)
	dstAmt, _ := decimal.NewFromString(amount)
	rate := decimal.Zero
	if !srcAmt.IsZero() {
		rate = dstAmt.Div(srcAmt)
	}

	return &Quote{
		SourceAsset:  sourceAsset,
		DestAsset:    destAsset,
		SourceAmount: srcAmt,
		DestAmount:   dstAmt,
		Rate:         rate,
		ExpiresAt:    time.Now().UTC().Add(30 * time.Second),
	}, nil
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
		Rate:         quote.Rate,
		CreatedAt:    time.Now().UTC(),
	}

	if err := s.conversionRepo.Create(ctx, conv); err != nil {
		return nil, fmt.Errorf("persist conversion: %w", err)
	}

	return conv, nil
}
