package fees

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

// estimateRepo is a minimal Repository double: the estimator only needs the
// schedule and tier lookups.
type estimateRepo struct {
	Repository
	feeBps int
}

func (m *estimateRepo) GetSchedule(_ context.Context, _ *string, _ string) (*domain.FeeSchedule, error) {
	return &domain.FeeSchedule{
		TransferFeeBps:   m.feeBps,
		ConversionFeeBps: m.feeBps,
		MinFeeAmount:     decimal.Zero,
	}, nil
}

func (m *estimateRepo) GetMonthlyVolume(_ context.Context, _ string) (decimal.Decimal, error) {
	return decimal.Zero, nil
}

func (m *estimateRepo) GetApplicableTier(_ context.Context, _ string, _ decimal.Decimal) *domain.FeeTier {
	return nil
}

type erroringFeeSource struct{}

func (erroringFeeSource) BaseFeeStroops(context.Context) (int64, error) {
	return 0, errors.New("horizon unavailable")
}

func TestEstimate_TransferCombinesPlatformAndNetworkFees(t *testing.T) {
	svc := NewEstimatorService(&estimateRepo{feeBps: 100}, StaticNetworkFeeSource{BaseFee: 100})

	estimate, err := svc.(Estimator).Estimate(context.Background(), "tenant-1", EstimateRequest{
		Type:   "transfer",
		Asset:  "USDC",
		Amount: decimal.RequireFromString("1000"),
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}

	if estimate.PlatformFee != "10.0000000" {
		t.Fatalf("platform fee = %s, want 10.0000000", estimate.PlatformFee)
	}
	if estimate.NetworkFeeStroops != 100 {
		t.Fatalf("network fee stroops = %d, want 100", estimate.NetworkFeeStroops)
	}
	if estimate.NetworkFee != "0.0000100" {
		t.Fatalf("network fee = %s, want 0.0000100", estimate.NetworkFee)
	}
	if estimate.TotalFee != "10.0000100" {
		t.Fatalf("total fee = %s, want 10.0000100", estimate.TotalFee)
	}
	if estimate.OperationCount != 1 || estimate.TransactionCount != 1 {
		t.Fatalf("ops/tx = %d/%d, want 1/1", estimate.OperationCount, estimate.TransactionCount)
	}
	if estimate.ExpiresAt.IsZero() {
		t.Fatal("estimate should carry an expiry")
	}
}

func TestEstimate_BatchPacksTransactionsAtProtocolCap(t *testing.T) {
	svc := NewEstimatorService(&estimateRepo{feeBps: 0}, StaticNetworkFeeSource{BaseFee: 100})

	estimate, err := svc.(Estimator).Estimate(context.Background(), "tenant-1", EstimateRequest{
		Type:         "batch",
		Asset:        "XLM",
		Amount:       decimal.RequireFromString("250"),
		Destinations: 250,
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}

	if estimate.OperationCount != 250 {
		t.Fatalf("operation count = %d, want 250", estimate.OperationCount)
	}
	// 250 operations at 100 per transaction need three transactions.
	if estimate.TransactionCount != 3 {
		t.Fatalf("transaction count = %d, want 3", estimate.TransactionCount)
	}
	if estimate.NetworkFeeStroops != 25_000 {
		t.Fatalf("network fee stroops = %d, want 25000", estimate.NetworkFeeStroops)
	}
	if estimate.NetworkFee != "0.0025000" {
		t.Fatalf("network fee = %s, want 0.0025000", estimate.NetworkFee)
	}
}

func TestEstimate_FallsBackToProtocolMinimumWhenSourceFails(t *testing.T) {
	svc := NewEstimatorService(&estimateRepo{feeBps: 0}, erroringFeeSource{})

	estimate, err := svc.(Estimator).Estimate(context.Background(), "tenant-1", EstimateRequest{
		Type:   "transfer",
		Asset:  "XLM",
		Amount: decimal.RequireFromString("1"),
	})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if estimate.BaseFeeStroops != DefaultBaseFeeStroops {
		t.Fatalf("base fee = %d, want the deterministic default %d", estimate.BaseFeeStroops, DefaultBaseFeeStroops)
	}
}

func TestEstimate_RejectsInvalidRequests(t *testing.T) {
	svc := NewEstimatorService(&estimateRepo{feeBps: 0}, StaticNetworkFeeSource{})

	cases := map[string]EstimateRequest{
		"unknown type":         {Type: "conversion", Asset: "XLM", Amount: decimal.NewFromInt(1)},
		"empty asset":          {Type: "transfer", Asset: "  ", Amount: decimal.NewFromInt(1)},
		"zero amount":          {Type: "transfer", Asset: "XLM", Amount: decimal.Zero},
		"negative amount":      {Type: "transfer", Asset: "XLM", Amount: decimal.NewFromInt(-5)},
		"batch no dests":       {Type: "batch", Asset: "XLM", Amount: decimal.NewFromInt(1)},
		"batch too many dests": {Type: "batch", Asset: "XLM", Amount: decimal.NewFromInt(1), Destinations: MaxEstimateOperations + 1},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.(Estimator).Estimate(context.Background(), "tenant-1", req); !errors.Is(err, ErrInvalidEstimateRequest) {
				t.Fatalf("err = %v, want ErrInvalidEstimateRequest", err)
			}
		})
	}
}

func TestHorizonNetworkFeeSource_UsesSurgeBaseFeeAndFloor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fee_stats" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"last_ledger_base_fee":"100","max_fee":{"last_ledger_base_fee":"500"}}`))
	}))
	defer server.Close()

	source := NewHorizonNetworkFeeSource(server.URL, 0)
	fee, err := source.BaseFeeStroops(context.Background())
	if err != nil {
		t.Fatalf("BaseFeeStroops: %v", err)
	}
	if fee != 500 {
		t.Fatalf("base fee = %d, want the surge max 500", fee)
	}

	// A below-minimum value is floored to the protocol minimum.
	floorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"last_ledger_base_fee":"1","max_fee":{"last_ledger_base_fee":"1"}}`))
	}))
	defer floorServer.Close()
	floor, err := NewHorizonNetworkFeeSource(floorServer.URL, 0).BaseFeeStroops(context.Background())
	if err != nil {
		t.Fatalf("floor BaseFeeStroops: %v", err)
	}
	if floor != DefaultBaseFeeStroops {
		t.Fatalf("base fee = %d, want the protocol minimum %d", floor, DefaultBaseFeeStroops)
	}

	// An unreachable Horizon returns the configured fallback, never an error.
	fallback, err := NewHorizonNetworkFeeSource("http://127.0.0.1:1", 250).BaseFeeStroops(context.Background())
	if err != nil {
		t.Fatalf("fallback BaseFeeStroops: %v", err)
	}
	if fallback != 250 {
		t.Fatalf("base fee = %d, want the fallback 250", fallback)
	}
}

func TestHandler_EstimateEndpoint(t *testing.T) {
	svc := NewEstimatorService(&estimateRepo{feeBps: 100}, StaticNetworkFeeSource{BaseFee: 100})
	h := NewHandler(svc)

	router := chi.NewRouter()
	router.Route("/fees", h.Routes())
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(tenant.WithID(r.Context(), "tenant-1"))
		router.ServeHTTP(w, r)
	})

	body := `{"type":"transfer","asset":"USDC","amount":"1000"}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/fees/estimate", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var estimate NetworkFeeEstimate
	if err := json.Unmarshal(rec.Body.Bytes(), &estimate); err != nil {
		t.Fatalf("decode estimate: %v", err)
	}
	if estimate.NetworkFeeStroops != 100 || estimate.PlatformFee != "10.0000000" {
		t.Fatalf("unexpected estimate: %+v", estimate)
	}

	// A batch without destinations is a client error, not an estimate.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/fees/estimate", strings.NewReader(`{"type":"batch","asset":"USDC","amount":"10"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid batch status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_EstimateUnavailableWithoutEstimator(t *testing.T) {
	// A Service that is not an Estimator must not panic; the endpoint is absent.
	h := NewHandler(serviceOnly{})
	router := chi.NewRouter()
	router.Route("/fees", h.Routes())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/fees/estimate", strings.NewReader(`{}`))
	req = req.WithContext(tenant.WithID(req.Context(), "tenant-1"))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no estimator is wired", rec.Code)
	}
}

// serviceOnly satisfies Service without implementing Estimator.
type serviceOnly struct{}

func (serviceOnly) GetSchedule(context.Context, string) (*domain.FeeSchedule, error) {
	return nil, nil
}
func (serviceOnly) SetSchedule(context.Context, *domain.FeeSchedule) error { return nil }
func (serviceOnly) CalculateTransferFee(context.Context, string, string, decimal.Decimal) (*TransferFee, error) {
	return nil, nil
}
func (serviceOnly) CalculateConversionFee(context.Context, string, string, decimal.Decimal) (*TransferFee, error) {
	return nil, nil
}
func (serviceOnly) RecordCollection(context.Context, *domain.FeeCollection) error { return nil }
func (serviceOnly) ListCollected(context.Context, *time.Time, *time.Time, *string, int, int) ([]*domain.FeeCollection, error) {
	return nil, nil
}

func TestOperationCount_CeilingsAndGuards(t *testing.T) {
	if ops, tx := operationCount(EstimateRequest{Type: "transfer"}, 100); ops != 1 || tx != 1 {
		t.Fatalf("transfer ops/tx = %d/%d, want 1/1", ops, tx)
	}
	if _, tx := operationCount(EstimateRequest{Type: "batch", Destinations: 100}, 100); tx != 1 {
		t.Fatalf("exact cap tx = %d, want 1", tx)
	}
	if _, tx := operationCount(EstimateRequest{Type: "batch", Destinations: 101}, 100); tx != 2 {
		t.Fatalf("one over cap tx = %d, want 2", tx)
	}
	// A non-positive cap must not divide by zero.
	if _, tx := operationCount(EstimateRequest{Type: "batch", Destinations: 3}, 0); tx != 1 {
		t.Fatalf("zero cap tx = %d, want 1", tx)
	}
}
