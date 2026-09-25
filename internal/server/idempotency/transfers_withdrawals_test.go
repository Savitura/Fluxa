package idempotency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/server/idempotency"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type memoryIdemRepo struct {
	mu      sync.Mutex
	records map[string]*idempotency.Record
}

func newMemoryIdemRepo() *memoryIdemRepo {
	return &memoryIdemRepo{records: make(map[string]*idempotency.Record)}
}

func (m *memoryIdemRepo) TryAcquire(_ context.Context, orgID, key, requestHash string, expiresAt time.Time) (*idempotency.Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := orgID + ":" + key
	if rec, ok := m.records[k]; ok {
		if !rec.ExpiresAt.IsZero() && time.Now().After(rec.ExpiresAt) {
			delete(m.records, k)
		} else {
			cp := *rec
			return &cp, true, nil
		}
	}

	m.records[k] = &idempotency.Record{
		OrgID:       orgID,
		Key:         key,
		RequestHash: requestHash,
		Status:      idempotency.StatusProcessing,
		ExpiresAt:   expiresAt,
	}
	return nil, false, nil
}

func (m *memoryIdemRepo) Complete(_ context.Context, orgID, key string, responseStatus int, responseBody []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := orgID + ":" + key
	rec, ok := m.records[k]
	if !ok {
		return nil
	}
	rec.Status = idempotency.StatusComplete
	rec.ResponseStatus = responseStatus
	rec.ResponseBody = responseBody
	return nil
}

func (m *memoryIdemRepo) expireKey(orgID, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := orgID + ":" + idempotency.DeterministicKey(key)
	if rec, ok := m.records[k]; ok {
		rec.ExpiresAt = time.Now().Add(-1 * time.Hour)
	}
}

// setupTransfersAndWithdrawalsRouter simulates the API router with idempotency middleware on transfers and withdrawals
func setupTransfersAndWithdrawalsRouter(repo idempotency.Repository, transferCount, withdrawalCount *int) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := tenant.WithID(r.Context(), "org-test-wave")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	idemMW := idempotency.OptionalMiddleware(repo)

	// POST /transfers
	r.With(idemMW).Post("/transfers", func(w http.ResponseWriter, r *http.Request) {
		*transferCount++
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":     "tx-" + uuid.New().String(),
			"status": "pending",
			"asset":  req["asset"],
			"amount": req["amount"],
		})
	})

	// POST /withdrawals
	r.With(idemMW).Post("/withdrawals", func(w http.ResponseWriter, r *http.Request) {
		*withdrawalCount++
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":       "wit-" + uuid.New().String(),
			"status":   "queued",
			"currency": req["currency"],
			"amount":   req["amount"],
		})
	})

	return r
}

func TestTransfersEndpointIdempotency(t *testing.T) {
	repo := newMemoryIdemRepo()
	var transferCount, withdrawalCount int
	router := setupTransfersAndWithdrawalsRouter(repo, &transferCount, &withdrawalCount)

	t.Run("POST /transfers with new X-Idempotency-Key processes transfer", func(t *testing.T) {
		key := "transfer-ref-001"
		body := `{"from_wallet_id":"` + uuid.New().String() + `","to_wallet_id":"` + uuid.New().String() + `","asset":"USDC","amount":"100.50"}`

		req := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Idempotency-Key", key)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected 202 Accepted, got %d: %s", rec.Code, rec.Body.String())
		}
		if transferCount != 1 {
			t.Fatalf("expected transferCount 1, got %d", transferCount)
		}
	})

	t.Run("POST /transfers replay with same X-Idempotency-Key returns cached response byte-for-byte", func(t *testing.T) {
		key := "transfer-ref-002"
		body := `{"from_wallet_id":"` + uuid.New().String() + `","to_wallet_id":"` + uuid.New().String() + `","asset":"USDC","amount":"50.00"}`

		// First call
		req1 := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewBufferString(body))
		req1.Header.Set("Content-Type", "application/json")
		req1.Header.Set("X-Idempotency-Key", key)
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)

		initialCount := transferCount

		// Retry with exact same body and key
		req2 := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewBufferString(body))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("X-Idempotency-Key", key)
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		if rec2.Code != rec1.Code {
			t.Fatalf("replayed status differs: got %d, expected %d", rec2.Code, rec1.Code)
		}
		if rec2.Body.String() != rec1.Body.String() {
			t.Fatalf("replayed body differs: got %s, expected %s", rec2.Body.String(), rec1.Body.String())
		}
		if transferCount != initialCount {
			t.Fatalf("handler should not have executed on replay, count went from %d to %d", initialCount, transferCount)
		}
	})

	t.Run("POST /transfers replay with same key but different body returns 409 Conflict", func(t *testing.T) {
		key := "transfer-ref-003"
		body1 := `{"from_wallet_id":"` + uuid.New().String() + `","to_wallet_id":"` + uuid.New().String() + `","asset":"USDC","amount":"25.00"}`
		body2 := `{"from_wallet_id":"` + uuid.New().String() + `","to_wallet_id":"` + uuid.New().String() + `","asset":"USDC","amount":"75.00"}`

		// Initial request
		req1 := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewBufferString(body1))
		req1.Header.Set("Content-Type", "application/json")
		req1.Header.Set("X-Idempotency-Key", key)
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)

		if rec1.Code != http.StatusAccepted {
			t.Fatalf("expected 202 Accepted, got %d", rec1.Code)
		}

		// Conflicting request with altered body
		req2 := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewBufferString(body2))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("X-Idempotency-Key", key)
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusConflict {
			t.Fatalf("expected 409 Conflict on altered body, got %d: %s", rec2.Code, rec2.Body.String())
		}
	})

	t.Run("POST /transfers with omitted X-Idempotency-Key passes without error (optional)", func(t *testing.T) {
		initialCount := transferCount
		body := `{"from_wallet_id":"` + uuid.New().String() + `","to_wallet_id":"` + uuid.New().String() + `","asset":"USDC","amount":"10.00"}`

		req := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		// No idempotency key header

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected 202 Accepted when key omitted, got %d", rec.Code)
		}
		if transferCount != initialCount+1 {
			t.Fatalf("expected transferCount to increment by 1, got %d", transferCount)
		}
	})

	t.Run("POST /transfers expired key allows re-execution", func(t *testing.T) {
		key := "transfer-ref-ttl-test"
		body := `{"from_wallet_id":"` + uuid.New().String() + `","to_wallet_id":"` + uuid.New().String() + `","asset":"USDC","amount":"12.00"}`

		// Initial request
		req1 := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewBufferString(body))
		req1.Header.Set("Content-Type", "application/json")
		req1.Header.Set("X-Idempotency-Key", key)
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)

		initialCount := transferCount

		// Expire the key in repo (>24 hours)
		repo.expireKey("org-test-wave", key)

		// Second request should execute again as a fresh request
		req2 := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewBufferString(body))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("X-Idempotency-Key", key)
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusAccepted {
			t.Fatalf("expected 202 Accepted on expired key re-run, got %d", rec2.Code)
		}
		if transferCount != initialCount+1 {
			t.Fatalf("expected handler to re-run after key expired, got %d calls", transferCount)
		}
	})
}

func TestWithdrawalsEndpointIdempotency(t *testing.T) {
	repo := newMemoryIdemRepo()
	var transferCount, withdrawalCount int
	router := setupTransfersAndWithdrawalsRouter(repo, &transferCount, &withdrawalCount)

	t.Run("POST /withdrawals with X-Idempotency-Key succeeds", func(t *testing.T) {
		key := "withdraw-ref-001"
		body := `{"wallet_id":"` + uuid.New().String() + `","amount":"500.00","currency":"NGN","account_bank":"044","account_number":"0123456789"}`

		req := httptest.NewRequest(http.MethodPost, "/withdrawals", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Idempotency-Key", key)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
		}
		if withdrawalCount != 1 {
			t.Fatalf("expected withdrawalCount 1, got %d", withdrawalCount)
		}
	})

	t.Run("POST /withdrawals replay returns cached response", func(t *testing.T) {
		key := "withdraw-ref-002"
		body := `{"wallet_id":"` + uuid.New().String() + `","amount":"250.00","currency":"NGN","account_bank":"044","account_number":"0123456789"}`

		req1 := httptest.NewRequest(http.MethodPost, "/withdrawals", bytes.NewBufferString(body))
		req1.Header.Set("Content-Type", "application/json")
		req1.Header.Set("X-Idempotency-Key", key)
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)

		initialCount := withdrawalCount

		req2 := httptest.NewRequest(http.MethodPost, "/withdrawals", bytes.NewBufferString(body))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("X-Idempotency-Key", key)
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		if rec2.Code != rec1.Code {
			t.Fatalf("replayed status mismatch: %d != %d", rec2.Code, rec1.Code)
		}
		if rec2.Body.String() != rec1.Body.String() {
			t.Fatalf("replayed body mismatch: %s != %s", rec2.Body.String(), rec1.Body.String())
		}
		if withdrawalCount != initialCount {
			t.Fatalf("handler executed again on replay, count=%d", withdrawalCount)
		}
	})

	t.Run("POST /withdrawals altered body returns 409 Conflict", func(t *testing.T) {
		key := "withdraw-ref-003"
		body1 := `{"wallet_id":"` + uuid.New().String() + `","amount":"100.00","currency":"NGN","account_bank":"044","account_number":"0123456789"}`
		body2 := `{"wallet_id":"` + uuid.New().String() + `","amount":"900.00","currency":"NGN","account_bank":"044","account_number":"0123456789"}`

		req1 := httptest.NewRequest(http.MethodPost, "/withdrawals", bytes.NewBufferString(body1))
		req1.Header.Set("Content-Type", "application/json")
		req1.Header.Set("X-Idempotency-Key", key)
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)

		req2 := httptest.NewRequest(http.MethodPost, "/withdrawals", bytes.NewBufferString(body2))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("X-Idempotency-Key", key)
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusConflict {
			t.Fatalf("expected 409 Conflict, got %d: %s", rec2.Code, rec2.Body.String())
		}
	})

	t.Run("POST /withdrawals optional key omitted succeeds", func(t *testing.T) {
		initialCount := withdrawalCount
		body := `{"wallet_id":"` + uuid.New().String() + `","amount":"50.00","currency":"NGN","account_bank":"044","account_number":"0123456789"}`

		req := httptest.NewRequest(http.MethodPost, "/withdrawals", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK without key, got %d", rec.Code)
		}
		if withdrawalCount != initialCount+1 {
			t.Fatalf("expected withdrawalCount increment, got %d", withdrawalCount)
		}
	})
}
