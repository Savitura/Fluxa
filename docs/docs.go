// Package docs exposes the published OpenAPI document as a Go value.
//
// The document is embedded rather than read from disk so that the bytes served
// at /docs/openapi.yaml are exactly the bytes tools/openapicheck validated in CI.
// Reading it from the working directory meant the served contract depended on
// where the process happened to be started, and a deployment that shipped
// without the docs/ directory served a 404 for its own API reference.
package docs

import _ "embed"

// OpenAPIYAML is the contents of docs/openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPIYAML []byte
