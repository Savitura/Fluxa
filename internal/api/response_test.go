package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
)

func TestErrorContractIncludesEffectiveRequestIDAndStatus(t *testing.T) {
	tests := []struct {
		name       string
		requestID  string
		err        error
		wantStatus int
	}{
		{"echo supplied request ID", "req-client-123", domain.ErrLastOrgOwner, http.StatusConflict},
		{"generate missing request ID", "", errors.New("database connection secret"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
			if tc.requestID != "" {
				req.Header.Set("X-Request-ID", tc.requestID)
			}
			rec := httptest.NewRecorder()
			api.WriteError(rec, req, tc.err)

			var body struct {
				Error struct {
					Code      string `json:"code"`
					Message   string `json:"message"`
					Status    int    `json:"status"`
					RequestID string `json:"request_id"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if rec.Code != tc.wantStatus || body.Error.Status != tc.wantStatus {
				t.Fatalf("status mismatch: HTTP %d, body %d", rec.Code, body.Error.Status)
			}
			if body.Error.Code == "" || body.Error.Message == "" {
				t.Fatalf("incomplete error detail: %+v", body.Error)
			}
			if body.Error.RequestID == "" || body.Error.RequestID != rec.Header().Get("X-Request-ID") {
				t.Fatalf("request ID mismatch: body=%q header=%q", body.Error.RequestID, rec.Header().Get("X-Request-ID"))
			}
			if tc.wantStatus == http.StatusInternalServerError && body.Error.Message != "an unexpected error occurred" {
				t.Fatalf("internal error details leaked: %q", body.Error.Message)
			}
		})
	}
}
