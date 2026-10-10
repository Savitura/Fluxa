package apikey_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fluxa/fluxa/internal/apikey"
)

func TestAPIKeyHandlersUseStructuredErrors(t *testing.T) {
	handler := apikey.NewHandler(nil)
	cases := []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"create", handler.Create},
		{"list", handler.List},
		{"revoke", handler.Revoke},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/keys", nil)
			req.Header.Set("X-Request-ID", "req-apikey-test")
			rec := httptest.NewRecorder()
			tc.call(rec, req)

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
			if rec.Code != http.StatusUnauthorized || body.Error.Status != rec.Code {
				t.Fatalf("status mismatch: HTTP %d, body %d", rec.Code, body.Error.Status)
			}
			if body.Error.Code != "UNAUTHORIZED" || body.Error.RequestID != "req-apikey-test" {
				t.Fatalf("unexpected error detail: %+v", body.Error)
			}
			if rec.Header().Get("X-Request-ID") != body.Error.RequestID {
				t.Fatalf("request ID header mismatch: %q", rec.Header().Get("X-Request-ID"))
			}
		})
	}
}
