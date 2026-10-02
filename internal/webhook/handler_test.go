package webhook

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/go-chi/chi/v5"
	"strconv"
	"time"
)

func TestVerify_RawBodyPrecisionAndNonTrivialPayload(t *testing.T) {
	secret := "whsec_test_non_trivial"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	// Non-trivial payload with unicode, special chars, whitespace
	body := `{"event":"transfer.settled","data":{"amount":"150.00","currency":"XLM","note":"Café résumé 🎉 \n\t \r \"escaped\""}}`
	sig := sign(secret, timestamp, []byte(body))

	result := Verify(secret, timestamp, body, sig)
	if !result.Valid {
		Fatalf := t.Fatalf
		Fatalf("expected valid signature for non-trivial payload, got reason=%q", result.Reason)
	}

	// Tampering with a single character in the payload should fail
	tamperedBody := `{"event":"transfer.settled","data":{"amount":"150.01","currency":"XLM","note":"Café résumé 🎉 \n\t \r \"escaped\""}}`
	tamperedResult := Verify(secret, timestamp, tamperedBody, sig)
	if tamperedResult.Valid {
		t.Fatal("expected tampered non-trivial payload to be rejected")
	}
}
func TestRegisterEndpointSecretAndValidation(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, nil, nil, 120, true)
	h := NewHandler(svc)

	r := chi.NewRouter()
	r.Route("/v1/webhooks", func(r chi.Router) { h.RegisterRoutes(r) })

	// Test 1: Invalid event type is rejected
	bodyInvalid, _ := json.Marshal(map[string]interface{}{
		"url":    "https://example.com/webhook",
		"events": []string{"invalid.event"},
	})
	reqInvalid := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewReader(bodyInvalid))
	reqInvalid.Header.Set("Content-Type", "application/json")
	recInvalid := httptest.NewRecorder()
	r.ServeHTTP(recInvalid, reqInvalid)
	if recInvalid.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid event, got %d", recInvalid.Code)
	}

	// Test 2: Valid registration returns secret once
	bodyValid, _ := json.Marshal(map[string]interface{}{
		"url":    "https://example.com/webhook",
		"events": []string{domain.EventTransferSettled},
	})
	reqValid := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewReader(bodyValid))
	reqValid.Header.Set("Content-Type", "application/json")
	recValid := httptest.NewRecorder()
	r.ServeHTTP(recValid, reqValid)
	if recValid.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", recValid.Code, recValid.Body.String())
	}

	var created domain.WebhookEndpoint
	if err := json.NewDecoder(recValid.Body).Decode(&created); err != nil {
		t.Fatalf("failed to decode created endpoint: %v", err)
	}

	if created.Secret == "" {
		t.Fatalf("expected non-empty secret on creation response")
	}

	// Test 3: Subsequent GET /v1/webhooks never returns the secret
	reqGet := httptest.NewRequest(http.MethodGet, "/v1/webhooks", nil)
	recGet := httptest.NewRecorder()
	r.ServeHTTP(recGet, reqGet)
	if recGet.Code != http.StatusOK {
		t.Fatalf("expected status 200 on GET, got %d", recGet.Code)
	}

	var listResp struct {
		Endpoints []domain.WebhookEndpoint `json:"endpoints"`
	}
	if err := json.NewDecoder(recGet.Body).Decode(&listResp); err != nil {
		t.Fatalf("failed to decode endpoints list: %v", err)
	}

	if len(listResp.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(listResp.Endpoints))
	}
	if listResp.Endpoints[0].Secret != "" {
		t.Fatalf("expected secret to be empty in GET response, got %q", listResp.Endpoints[0].Secret)
	}
}
