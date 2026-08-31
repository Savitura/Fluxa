package batch

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// supportedAssets is a test asset registry.
var supportedAssets = map[string]bool{
	"XLM":  true,
	"USDC": true,
	"EURC": true,
}

// testValidationError matches the JSON shape returned by the API.
type testValidationError struct {
	Row    int    `json:"row"`
	Field  string `json:"field"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// testErrorResponse matches the error envelope returned by the API.
type testErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	ValidationErrors []testValidationError `json:"validation_errors"`
}

func TestCreateBatch_AllValidAssets_Succeeds(t *testing.T) {
	txRepo := newFakeTxRepo()
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: map[string]bool{}}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)
	h := NewHandler(svc).WithAssetValidator(func(code string) bool {
		return supportedAssets[code]
	})

	body := `{
		"from_wallet_id": "550e8400-e29b-41d4-a716-446655440000",
		"transfers": [
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440001", "asset": "XLM", "amount": "10.0000000"},
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440002", "asset": "USDC", "amount": "25.0000000"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/batch/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.createBatch(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp batchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.TotalCount != 2 {
		t.Fatalf("total_count = %d, want 2", resp.TotalCount)
	}
	if len(resp.ValidationErrors) != 0 {
		t.Fatalf("expected no validation errors, got %d", len(resp.ValidationErrors))
	}
}

func TestCreateBatch_InvalidAssetCode_Returns400WithValidationErrors(t *testing.T) {
	txRepo := newFakeTxRepo()
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: map[string]bool{}}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)
	h := NewHandler(svc).WithAssetValidator(func(code string) bool {
		return supportedAssets[code]
	})

	body := `{
		"from_wallet_id": "550e8400-e29b-41d4-a716-446655440000",
		"transfers": [
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440001", "asset": "DOGE", "amount": "10.0000000"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/batch/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.createBatch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp testErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Code != "BAD_REQUEST" {
		t.Fatalf("error code = %q, want BAD_REQUEST", resp.Error.Code)
	}
	if len(resp.ValidationErrors) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(resp.ValidationErrors))
	}
	ve := resp.ValidationErrors[0]
	if ve.Row != 1 {
		t.Fatalf("validation error row = %d, want 1", ve.Row)
	}
	if ve.Field != "asset" {
		t.Fatalf("validation error field = %q, want asset", ve.Field)
	}
	if ve.Value != "DOGE" {
		t.Fatalf("validation error value = %q, want DOGE", ve.Value)
	}
	if ve.Reason != "unsupported asset code" {
		t.Fatalf("validation error reason = %q, want 'unsupported asset code'", ve.Reason)
	}
}

func TestCreateBatch_MixOfValidAndInvalidAssets_ReturnsAllErrors(t *testing.T) {
	txRepo := newFakeTxRepo()
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: map[string]bool{}}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)
	h := NewHandler(svc).WithAssetValidator(func(code string) bool {
		return supportedAssets[code]
	})

	body := `{
		"from_wallet_id": "550e8400-e29b-41d4-a716-446655440000",
		"transfers": [
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440001", "asset": "XLM", "amount": "10.0000000"},
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440002", "asset": "DOGE", "amount": "5.0000000"},
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440003", "asset": "SHIB", "amount": "100.0000000"},
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440004", "asset": "USDC", "amount": "50.0000000"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/batch/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.createBatch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp testErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.ValidationErrors) != 2 {
		t.Fatalf("expected 2 validation errors, got %d", len(resp.ValidationErrors))
	}

	// Row 2 (DOGE) and Row 3 (SHIB) should be flagged.
	for _, ve := range resp.ValidationErrors {
		if ve.Field != "asset" {
			t.Errorf("expected field 'asset', got %q", ve.Field)
		}
		if ve.Reason != "unsupported asset code" {
			t.Errorf("expected reason 'unsupported asset code', got %q", ve.Reason)
		}
	}
}

func TestCreateBatch_InvalidAssetCode_NoBatchCreated(t *testing.T) {
	txRepo := newFakeTxRepo()
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: map[string]bool{}}
	batchRepo := newFakeBatchRepo()
	svc := NewService(batchRepo, txRepo, transferSvc)
	h := NewHandler(svc).WithAssetValidator(func(code string) bool {
		return supportedAssets[code]
	})

	body := `{
		"from_wallet_id": "550e8400-e29b-41d4-a716-446655440000",
		"transfers": [
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440001", "asset": "DOGE", "amount": "10.0000000"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/batch/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.createBatch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(batchRepo.batches) != 0 {
		t.Fatalf("expected no batches created, got %d", len(batchRepo.batches))
	}
	if len(txRepo.byBatch) != 0 {
		t.Fatalf("expected no transactions created, got %d", len(txRepo.byBatch))
	}
}

func TestCreateBatch_NoAssetValidator_BypassesValidation(t *testing.T) {
	txRepo := newFakeTxRepo()
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: map[string]bool{}}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)
	// No WithAssetValidator — validation is skipped.
	h := NewHandler(svc)

	body := `{
		"from_wallet_id": "550e8400-e29b-41d4-a716-446655440000",
		"transfers": [
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440001", "asset": "DOGE", "amount": "10.0000000"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/batch/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.createBatch(rec, req)

	// Without a validator, the batch is created even with an invalid asset.
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateBatch_MultipleInvalidAssets_ReturnsCorrectRowNumbers(t *testing.T) {
	txRepo := newFakeTxRepo()
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: map[string]bool{}}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)
	h := NewHandler(svc).WithAssetValidator(func(code string) bool {
		return supportedAssets[code]
	})

	body := `{
		"from_wallet_id": "550e8400-e29b-41d4-a716-446655440000",
		"transfers": [
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440001", "asset": "XLM", "amount": "10.0000000"},
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440002", "asset": "DOGE", "amount": "5.0000000"},
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440003", "asset": "XLM", "amount": "20.0000000"},
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440004", "asset": "PEPE", "amount": "1.0000000"},
			{"to_wallet_id": "550e8400-e29b-41d4-a716-446655440005", "asset": "XLM", "amount": "30.0000000"}
		]
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/batch/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.createBatch(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp testErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.ValidationErrors) != 2 {
		t.Fatalf("expected 2 validation errors, got %d", len(resp.ValidationErrors))
	}

	// Row 2 = DOGE, Row 4 = PEPE
	rows := map[int]string{}
	for _, ve := range resp.ValidationErrors {
		rows[ve.Row] = ve.Value
	}
	if rows[2] != "DOGE" {
		t.Errorf("row 2 = %q, want DOGE", rows[2])
	}
	if rows[4] != "PEPE" {
		t.Errorf("row 4 = %q, want PEPE", rows[4])
	}
}
