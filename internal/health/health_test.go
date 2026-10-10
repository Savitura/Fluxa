package health

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerDoesNotExposeProbeError(t *testing.T) {
	handler := New(map[string]Probe{"postgres": func(context.Context) (interface{}, error) { return nil, errors.New("password=hidden") }}).Handler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/health", nil))
	if strings.Contains(recorder.Body.String(), "hidden") || !strings.Contains(recorder.Body.String(), "probe failed") {
		t.Fatalf("health response did not sanitize probe failure: %s", recorder.Body.String())
	}
}
