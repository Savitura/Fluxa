package fx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
)

func TestCoinGeckoProvider_XLMToUSDC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stellar":{"usd":0.25,"eur":0.23}}`))
	}))
	defer srv.Close()

	p := NewCoinGeckoProviderWithURL(srv.URL, []string{"XLM-USDC", "USDC-XLM"})
	rate, err := p.GetRate(context.Background(), "XLM", "USDC", "1")
	if err != nil {
		t.Fatalf("GetRate: %v", err)
	}
	want := decimal.RequireFromString("0.25")
	if !rate.Equal(want) {
		t.Fatalf("rate = %s, want %s", rate, want)
	}
}

func TestCoinGeckoProvider_USDCToXLM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"stellar":{"usd":0.25,"eur":0.23}}`))
	}))
	defer srv.Close()

	p := NewCoinGeckoProviderWithURL(srv.URL, []string{"USDC-XLM"})
	rate, err := p.GetRate(context.Background(), "USDC", "XLM", "1")
	if err != nil {
		t.Fatalf("GetRate: %v", err)
	}
	want := decimal.NewFromInt(1).Div(decimal.RequireFromString("0.25"))
	if !rate.Equal(want) {
		t.Fatalf("rate = %s, want %s", rate, want)
	}
}

func TestCoinGeckoProvider_UnsupportedPair(t *testing.T) {
	p := NewCoinGeckoProvider([]string{"XLM-USDC"})
	_, err := p.GetRate(context.Background(), "USDC", "EURC", "1")
	if err == nil {
		t.Fatal("expected error for unsupported pair")
	}
}

func TestIsNativeAssetAndTrustline(t *testing.T) {
	if !IsNativeAsset("xlm") || !IsNativeAsset("XLM") {
		t.Fatal("expected XLM to be native")
	}
	if AssetRequiresTrustline("XLM") {
		t.Fatal("XLM must not require a trustline")
	}
	if !AssetRequiresTrustline("USDC") {
		t.Fatal("USDC must require a trustline")
	}
}

func TestValidateAmountLimits_XLM(t *testing.T) {
	if err := validateAmountLimits("XLM", decimal.RequireFromString("0.0000001")); err != nil {
		t.Fatalf("min amount should pass: %v", err)
	}
	if err := validateAmountLimits("XLM", decimal.RequireFromString("0.00000005")); err == nil {
		t.Fatal("below min should fail")
	}
	if err := validateAmountLimits("XLM", decimal.NewFromInt(1_000_001)); err == nil {
		t.Fatal("above max should fail")
	}
	if err := validateAmountLimits("USDC", decimal.NewFromInt(1_000_001)); err != nil {
		t.Fatalf("non-XLM should skip XLM limits: %v", err)
	}
}
