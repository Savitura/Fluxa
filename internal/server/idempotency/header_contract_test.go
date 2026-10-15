package idempotency

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetIdempotencyKey pins the header contract for issue #410: Idempotency-Key
// is canonical, X-Idempotency-Key is an alias, and sending both with different
// values is an error instead of an endpoint-dependent precedence question.
func TestGetIdempotencyKey(t *testing.T) {
	cases := []struct {
		name      string
		canonical string
		alias     string
		want      string
		wantErr   bool
	}{
		{name: "no headers", want: ""},
		{name: "canonical only", canonical: "key-1", want: "key-1"},
		{name: "alias only", alias: "key-2", want: "key-2"},
		{name: "both equal", canonical: "key-3", alias: "key-3", want: "key-3"},
		{name: "both different", canonical: "key-4", alias: "key-5", wantErr: true},
		{name: "whitespace is trimmed", canonical: "  key-6  ", want: "key-6"},
		{name: "canonical wins over empty alias", canonical: "key-7", alias: "   ", want: "key-7"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/transfers", nil)
			if tc.canonical != "" {
				req.Header.Set(headerKey, tc.canonical)
			}
			if tc.alias != "" {
				req.Header.Set(xHeaderKey, tc.alias)
			}

			got, err := GetIdempotencyKey(req)
			if tc.wantErr {
				if !errors.Is(err, ErrConflictingKeys) {
					t.Fatalf("want ErrConflictingKeys, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestExtractKeyKeepsCanonicalOnConflict documents that the legacy helper still
// returns a usable key when both headers are present but disagree.
func TestExtractKeyKeepsCanonicalOnConflict(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers", nil)
	req.Header.Set(headerKey, "canonical")
	req.Header.Set(xHeaderKey, "alias")

	if got := ExtractKey(req); got != "canonical" {
		t.Fatalf("want canonical, got %q", got)
	}
}

// TestMiddlewareRejectsConflictingHeaders checks the behaviour a client sees.
// The nil repository is safe here: a conflicting pair is rejected before the
// middleware touches storage.
func TestMiddlewareRejectsConflictingHeaders(t *testing.T) {
	handler := RequiredMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/transfers", nil)
	req.Header.Set(headerKey, "one")
	req.Header.Set(xHeaderKey, "two")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}
