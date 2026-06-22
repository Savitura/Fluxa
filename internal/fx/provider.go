package fx

import (
	"context"
	"fmt"

	horizonclient "github.com/stellar/go/clients/horizonclient"
	"github.com/shopspring/decimal"
)

// Provider fetches mid-market FX rates for a set of currency pairs.
// Swapping the implementation (e.g. Horizon → third-party oracle) requires
// only changing the concrete type passed to NewService — no handler changes.
type Provider interface {
	// GetRate returns the mid-market rate: units of `to` per one unit of `from`.
	GetRate(ctx context.Context, from, to, amount string) (decimal.Decimal, error)
	// SupportedPairs returns supported pairs in "FROM-TO" format.
	SupportedPairs() []string
}

// HorizonProvider fetches rates from the Stellar DEX order book via Horizon.
type HorizonProvider struct {
	client  *horizonclient.Client
	pairs   []string
	issuers map[string]string // asset code → issuer address; empty string for XLM
}

// NewHorizonProvider creates a Provider backed by the Stellar Horizon order book.
// pairs is a list of "FROM-TO" pair strings; issuers maps asset codes to their issuers.
func NewHorizonProvider(horizonURL string, pairs []string, issuers map[string]string) *HorizonProvider {
	return &HorizonProvider{
		client:  &horizonclient.Client{HorizonURL: horizonURL},
		pairs:   pairs,
		issuers: issuers,
	}
}

func (p *HorizonProvider) SupportedPairs() []string {
	return p.pairs
}

// GetRate queries the Horizon order book for the best ask price (from → to).
func (p *HorizonProvider) GetRate(ctx context.Context, from, to, _ string) (decimal.Decimal, error) {
	req := horizonclient.OrderBookRequest{
		SellingAssetType: horizonAssetType(from),
		BuyingAssetType:  horizonAssetType(to),
	}
	if from != "XLM" {
		req.SellingAssetCode = from
		req.SellingAssetIssuer = p.issuers[from]
	}
	if to != "XLM" {
		req.BuyingAssetCode = to
		req.BuyingAssetIssuer = p.issuers[to]
	}

	book, err := p.client.OrderBook(req)
	if err != nil {
		return decimal.Zero, fmt.Errorf("order book %s-%s: %w", from, to, err)
	}
	if len(book.Asks) == 0 {
		return decimal.Zero, fmt.Errorf("no liquidity for %s-%s", from, to)
	}

	rate, err := decimal.NewFromString(book.Asks[0].Price)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse ask price %q: %w", book.Asks[0].Price, err)
	}
	return rate, nil
}

func horizonAssetType(code string) horizonclient.AssetType {
	if code == "XLM" {
		return horizonclient.AssetTypeNative
	}
	if len(code) <= 4 {
		return horizonclient.AssetType4
	}
	return horizonclient.AssetType12
}
