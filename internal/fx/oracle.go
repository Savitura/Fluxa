package fx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	defaultCoinGeckoURL = "https://api.coingecko.com/api/v3/simple/price"
	oracleHTTPTimeout   = 8 * time.Second
)

// CoinGeckoProvider is an oracle fallback that prices XLM via CoinGecko
// when Stellar DEX order-book liquidity is unavailable.
type CoinGeckoProvider struct {
	baseURL string
	client  *http.Client
	pairs   []string
}

// NewCoinGeckoProvider creates an oracle provider for the given XLM pairs.
// pairs should use "FROM-TO" form (e.g. "XLM-USDC"). When empty, DefaultXLMFXPairs is used.
func NewCoinGeckoProvider(pairs []string) *CoinGeckoProvider {
	if len(pairs) == 0 {
		pairs = DefaultXLMFXPairs()
	}
	return &CoinGeckoProvider{
		baseURL: defaultCoinGeckoURL,
		client:  &http.Client{Timeout: oracleHTTPTimeout},
		pairs:   append([]string(nil), pairs...),
	}
}

// NewCoinGeckoProviderWithURL is used by tests to inject a mock base URL.
func NewCoinGeckoProviderWithURL(baseURL string, pairs []string) *CoinGeckoProvider {
	p := NewCoinGeckoProvider(pairs)
	p.baseURL = strings.TrimRight(baseURL, "/")
	return p
}

func (p *CoinGeckoProvider) SupportedPairs() []string {
	return p.pairs
}

// GetRate returns units of `to` per one unit of `from`, derived from CoinGecko
// XLM/USD and XLM/EUR spot prices (USDC≈USD, EURC≈EUR).
func (p *CoinGeckoProvider) GetRate(ctx context.Context, from, to, _ string) (decimal.Decimal, error) {
	from = normalizeAsset(from)
	to = normalizeAsset(to)
	pairKey := from + "-" + to
	supported := false
	for _, pair := range p.pairs {
		if pair == pairKey {
			supported = true
			break
		}
	}
	if !supported {
		return decimal.Zero, fmt.Errorf("oracle unsupported pair %s", pairKey)
	}

	prices, err := p.fetchPrices(ctx)
	if err != nil {
		return decimal.Zero, err
	}

	rate, err := rateFromOraclePrices(from, to, prices)
	if err != nil {
		return decimal.Zero, err
	}
	return rate, nil
}

type coinGeckoPriceResponse map[string]map[string]float64

func (p *CoinGeckoProvider) fetchPrices(ctx context.Context) (map[string]decimal.Decimal, error) {
	url := p.baseURL
	if !strings.Contains(url, "?") {
		url = fmt.Sprintf("%s?ids=stellar&vs_currencies=usd,eur", p.baseURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("oracle request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "fluxa-fx-oracle/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oracle fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oracle status %d", resp.StatusCode)
	}

	var body coinGeckoPriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("oracle decode: %w", err)
	}
	stellar, ok := body["stellar"]
	if !ok {
		return nil, fmt.Errorf("oracle missing stellar price")
	}

	out := make(map[string]decimal.Decimal, 2)
	if usd, ok := stellar["usd"]; ok && usd > 0 {
		out["USD"] = decimal.NewFromFloat(usd)
	}
	if eur, ok := stellar["eur"]; ok && eur > 0 {
		out["EUR"] = decimal.NewFromFloat(eur)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("oracle returned no usable XLM prices")
	}
	return out, nil
}

// rateFromOraclePrices maps XLM↔USDC/EURC using USD/EUR spot (stablecoin peg).
func rateFromOraclePrices(from, to string, prices map[string]decimal.Decimal) (decimal.Decimal, error) {
	switch {
	case from == "XLM" && to == "USDC":
		p, ok := prices["USD"]
		if !ok {
			return decimal.Zero, fmt.Errorf("oracle missing XLM/USD")
		}
		return p, nil
	case from == "USDC" && to == "XLM":
		p, ok := prices["USD"]
		if !ok {
			return decimal.Zero, fmt.Errorf("oracle missing XLM/USD")
		}
		return decimal.NewFromInt(1).Div(p), nil
	case from == "XLM" && to == "EURC":
		p, ok := prices["EUR"]
		if !ok {
			return decimal.Zero, fmt.Errorf("oracle missing XLM/EUR")
		}
		return p, nil
	case from == "EURC" && to == "XLM":
		p, ok := prices["EUR"]
		if !ok {
			return decimal.Zero, fmt.Errorf("oracle missing XLM/EUR")
		}
		return decimal.NewFromInt(1).Div(p), nil
	default:
		return decimal.Zero, fmt.Errorf("oracle cannot price %s-%s", from, to)
	}
}
