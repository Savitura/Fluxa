package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mockRouter struct {
	routes map[string]http.HandlerFunc
}

func newMockRouter() *mockRouter {
	return &mockRouter{routes: make(map[string]http.HandlerFunc)}
}

func (m *mockRouter) Get(pattern string, handler http.HandlerFunc) {
	m.routes[pattern] = handler
}

func (m *mockRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h, ok := m.routes[r.URL.Path]; ok {
		h(w, r)
		return
	}
	http.NotFound(w, r)
}

func TestRegisterDocsRoutes(t *testing.T) {
	r := newMockRouter()
	RegisterDocsRoutes(r)

	expectedRoutes := []string{
		"/",
		"/docs",
		"/docs/",
		"/docs/openapi.yaml",
	}

	for _, route := range expectedRoutes {
		if _, ok := r.routes[route]; !ok {
			t.Errorf("expected route %q to be registered, but it was not", route)
		}
	}
}

func TestRegisterRootRoute(t *testing.T) {
	r := newMockRouter()
	RegisterRootRoute(r)

	if _, ok := r.routes["/"]; !ok {
		t.Errorf("expected route %q to be registered by RegisterRootRoute", "/")
	}
}

func TestServeSwaggerUI(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec := httptest.NewRecorder()

	ServeSwaggerUI(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected Content-Type to contain text/html, got %q", contentType)
	}

	body := rec.Body.String()
	requiredSnippets := []string{
		`id="swagger-ui"`,
		`url: '/docs/openapi.yaml'`,
		`SwaggerUIBundle`,
		`persistAuthorization: true`,
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(body, snippet) {
			t.Errorf("expected Swagger UI HTML to contain %q, but it did not", snippet)
		}
	}
}

func TestServeSwaggerUI_TrailingSlash(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/docs/", nil)
	rec := httptest.NewRecorder()

	serveSwaggerUI(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("expected Content-Type to contain text/html, got %q", contentType)
	}
}

func TestServeOpenAPISpec(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/docs/openapi.yaml", nil)
	rec := httptest.NewRecorder()

	ServeOpenAPISpec(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/yaml") {
		t.Errorf("expected Content-Type to contain application/yaml, got %q", contentType)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "openapi:") && !strings.Contains(body, "Fluxa API") {
		t.Errorf("expected OpenAPI spec to contain openapi header or Fluxa API title, got: %s", body[:min(len(body), 200)])
	}
}

func TestServeOpenAPISpec_CustomEnv(t *testing.T) {
	tmpDir := t.TempDir()
	specFile := filepath.Join(tmpDir, "custom-spec.yaml")
	content := "openapi: 3.0.3\ninfo:\n  title: Custom Fluxa\n"
	if err := os.WriteFile(specFile, []byte(content), 0644); err != nil {
		t.Fatalf("write temp spec: %v", err)
	}

	t.Setenv("OPENAPI_SPEC_PATH", specFile)

	req := httptest.NewRequest(http.MethodGet, "/docs/openapi.yaml", nil)
	rec := httptest.NewRecorder()

	ServeOpenAPISpec(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "Custom Fluxa") {
		t.Errorf("expected spec loaded from OPENAPI_SPEC_PATH, got: %s", rec.Body.String())
	}
}

func TestServeOpenAPISpec_NotFound(t *testing.T) {
	// Point to a non-existent directory and non-existent spec
	t.Setenv("OPENAPI_SPEC_PATH", filepath.Join(t.TempDir(), "nonexistent.yaml"))
	t.Setenv("DOCS_PATH", filepath.Join(t.TempDir(), "nonexistent_dir"))

	// Change working directory to a clean empty temp dir
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	emptyDir := t.TempDir()
	if err := os.Chdir(emptyDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	req := httptest.NewRequest(http.MethodGet, "/docs/openapi.yaml", nil)
	rec := httptest.NewRecorder()

	ServeOpenAPISpec(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "OpenAPI spec not found") {
		t.Errorf("expected error message 'OpenAPI spec not found', got %q", rec.Body.String())
	}
}

func TestServeRoot(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	ServeRoot(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", contentType)
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode JSON response: %v", err)
	}

	if docsURL, ok := payload["docs_url"].(string); !ok || docsURL != "/docs" {
		t.Errorf("expected docs_url='/docs', got %v", payload["docs_url"])
	}

	if docs, ok := payload["docs"].(string); !ok || docs != "/docs" {
		t.Errorf("expected docs='/docs', got %v", payload["docs"])
	}

	if openapiURL, ok := payload["openapi_url"].(string); !ok || openapiURL != "/docs/openapi.yaml" {
		t.Errorf("expected openapi_url='/docs/openapi.yaml', got %v", payload["openapi_url"])
	}
}

func TestUnauthenticatedAccess(t *testing.T) {
	r := newMockRouter()
	RegisterDocsRoutes(r)

	paths := []string{
		"/",
		"/docs",
		"/docs/",
		"/docs/openapi.yaml",
	}

	for _, path := range paths {
		t.Run("GET "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			// Explicitly ensure no Authorization header is provided
			req.Header.Del("Authorization")
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("expected path %q to succeed with 200 without authentication, got %d", path, rec.Code)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
