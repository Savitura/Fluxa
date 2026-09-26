package fx

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/shopspring/decimal"
)

type mockFXService struct {
	getQuoteFunc          func(ctx context.Context, from, to, amount string) (*Quote, error)
	executeConversionFunc func(ctx context.Context, walletID, quoteID string) (*domain.Conversion, error)
	getRatesFunc          func(ctx context.Context, from, to string) (*RatesResponse, error)
}

func (m *mockFXService) GetQuote(ctx context.Context, from, to, amount string) (*Quote, error) {
	if m.getQuoteFunc != nil {
		return m.getQuoteFunc(ctx, from, to, amount)
	}
	return nil, nil
}

func (m *mockFXService) ExecuteConversion(ctx context.Context, walletID, quoteID string) (*domain.Conversion, error) {
	if m.executeConversionFunc != nil {
		return m.executeConversionFunc(ctx, walletID, quoteID)
	}
	return nil, nil
}

func (m *mockFXService) GetRates(ctx context.Context, from, to string) (*RatesResponse, error) {
	if m.getRatesFunc != nil {
		return m.getRatesFunc(ctx, from, to)
	}
	return nil, nil
}

func TestHandler_GetQuote(t *testing.T) {
	svc := &mockFXService{
		getQuoteFunc: func(ctx context.Context, from, to, amount string) (*Quote, error) {
			return &Quote{ID: "q-123"}, nil
		},
	}
	h := NewHandler(svc)

	body := []byte(`{"from_asset":"USDC","to_asset":"XLM","amount":"10"}`)
	req := httptest.NewRequest(http.MethodPost, "/quote", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.getQuote(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestHandler_Convert(t *testing.T) {
	svc := &mockFXService{
		executeConversionFunc: func(ctx context.Context, walletID, quoteID string) (*domain.Conversion, error) {
			return &domain.Conversion{ID: "c-123"}, nil
		},
	}
	h := NewHandler(svc)

	// We use a valid UUID to pass validation
	body := []byte(`{"wallet_id":"550e8400-e29b-41d4-a716-446655440000","quote_id":"550e8400-e29b-41d4-a716-446655440000"}`)
	req := httptest.NewRequest(http.MethodPost, "/convert", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.convert(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}
}

func TestHandler_GetRates(t *testing.T) {
	svc := &mockFXService{
		getRatesFunc: func(ctx context.Context, from, to string) (*RatesResponse, error) {
			return &RatesResponse{Rate: decimal.NewFromInt(2)}, nil
		},
	}
	h := NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/rates?from=USDC&to=XLM", nil)
	w := httptest.NewRecorder()

	h.getRates(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
