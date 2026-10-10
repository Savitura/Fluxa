package server

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/fluxa/fluxa/docs"
)

// DocsRouter is the minimal interface required to register documentation routes.
type DocsRouter interface {
	Get(string, http.HandlerFunc)
}

// RegisterDocsRoutes serves Swagger UI at /docs and the OpenAPI spec at
// /docs/openapi.yaml, and links to /docs in the root API response (#152).
func RegisterDocsRoutes(r DocsRouter) {
	r.Get("/", ServeRoot)
	r.Get("/docs", ServeSwaggerUI)
	r.Get("/docs/", ServeSwaggerUI)
	r.Get("/docs/openapi.yaml", ServeOpenAPISpec)
}

// RegisterRootRoute registers the root API endpoint that links to /docs.
func RegisterRootRoute(r DocsRouter) {
	r.Get("/", ServeRoot)
}

// ServeRoot handles GET / and returns basic API metadata with a link to the interactive docs.
func ServeRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"name":        "Fluxa API",
		"version":     "1.0.0",
		"description": "Multi-currency digital asset platform built on Stellar",
		"docs_url":    "/docs",
		"docs":        "/docs",
		"openapi_url": "/docs/openapi.yaml",
		"openapi":     "/docs/openapi.yaml",
	})
}

// ServeSwaggerUI serves the interactive Swagger UI page at /docs and /docs/.
func ServeSwaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(swaggerHTML))
}

// ServeOpenAPISpec serves the OpenAPI specification file at /docs/openapi.yaml.
func ServeOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	data, err := findOpenAPISpec()
	if err != nil {
		http.Error(w, "OpenAPI spec not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// Internal unexported aliases for backwards compatibility
func serveSwaggerUI(w http.ResponseWriter, r *http.Request) {
	ServeSwaggerUI(w, r)
}

func serveOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	ServeOpenAPISpec(w, r)
}

func serveRoot(w http.ResponseWriter, r *http.Request) {
	ServeRoot(w, r)
}

// findOpenAPISpec returns the OpenAPI document served at /docs/openapi.yaml.
// The canonical document is embedded in the binary (see package docs) so the
// bytes served match what tools/openapicheck validated in CI regardless of the
// process working directory. OPENAPI_SPEC_PATH overrides the embedded document
// for local experimentation; an override that cannot be read is an error rather
// than a silent fallback, so a misconfigured path is visible.
func findOpenAPISpec() ([]byte, error) {
	if envPath := os.Getenv("OPENAPI_SPEC_PATH"); envPath != "" {
		return os.ReadFile(envPath) // #nosec G304 -- intentional env-based override for local dev
	}
	return docs.OpenAPIYAML, nil
}

const swaggerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Fluxa API Documentation</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <link rel="icon" type="image/png" href="https://unpkg.com/swagger-ui-dist@5/favicon-32x32.png" sizes="32x32">
  <style>
    html {
      box-sizing: border-box;
      overflow: -moz-scrollbars-vertical;
      overflow-y: scroll;
    }
    *, *:before, *:after {
      box-sizing: inherit;
    }
    body {
      margin: 0;
      background: #fafafa;
    }
    .swagger-ui .topbar {
      display: block;
      background-color: #0f172a;
    }
    .swagger-ui .topbar a {
      max-width: 100%;
      font-weight: 700;
      color: #ffffff;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js" charset="UTF-8"></script>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-standalone-preset.js" charset="UTF-8"></script>
  <script>
    window.onload = function() {
      window.ui = SwaggerUIBundle({
        url: '/docs/openapi.yaml',
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [
          SwaggerUIBundle.presets.apis,
          SwaggerUIStandalonePreset
        ],
        plugins: [
          SwaggerUIBundle.plugins.DownloadUrl
        ],
        layout: 'StandaloneLayout',
        persistAuthorization: true,
        displayRequestDuration: true,
        filter: true,
        tryItOutEnabled: true,
        docExpansion: 'list'
      });
    };
  </script>
</body>
</html>`
