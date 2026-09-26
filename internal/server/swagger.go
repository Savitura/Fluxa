package server

import (
	"net/http"

	"github.com/fluxa/fluxa/docs"
)

// RegisterDocsRoutes serves Swagger UI at /docs and the OpenAPI spec at
// /docs/openapi.yaml (#152).
func RegisterDocsRoutes(r interface {
	Get(string, http.HandlerFunc)
}) {
	r.Get("/docs", serveSwaggerUI)
	r.Get("/docs/", serveSwaggerUI)
	r.Get("/docs/openapi.yaml", serveOpenAPISpec)
}

func serveSwaggerUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(swaggerHTML))
}

func serveOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	// Embedded rather than read from disk: this must be the same document CI
	// validated, and a process started outside the repository root used to
	// answer 404 here.
	w.Header().Set("Content-Type", "application/yaml")
	w.Write(docs.OpenAPIYAML)
}

const swaggerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Fluxa API Documentation</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
  <style>
    body { margin: 0; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = function() {
      window.ui = SwaggerUIBundle({
        url: '/docs/openapi.yaml',
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis],
        layout: 'BaseLayout',
      });
    };
  </script>
</body>
</html>`
