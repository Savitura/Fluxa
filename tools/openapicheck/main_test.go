package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goodDoc is a minimal document that satisfies every rule. Tests mutate a copy
// of it to prove one rule fires, so a rule that silently stops working shows up
// as a failing test rather than as a green pipeline.
const goodDoc = `openapi: 3.0.3
info:
  title: Test API
  version: 1.0.0
tags:
  - name: wallets
    description: Wallet operations
security:
  - BearerAuth: []
paths:
  /v1/wallets/{id}:
    parameters:
      - $ref: '#/components/parameters/WalletID'
    get:
      operationId: getWallet
      tags: [wallets]
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Wallet'
        '400':
          $ref: '#/components/responses/BadRequest'
        '401':
          $ref: '#/components/responses/Unauthorized'
        '500':
          $ref: '#/components/responses/InternalServerError'
components:
  securitySchemes:
    BearerAuth:
      type: http
      scheme: bearer
    AdminAuth:
      type: http
      scheme: bearer
  parameters:
    WalletID:
      name: id
      in: path
      required: true
      schema:
        type: string
  responses:
    BadRequest:
      description: Bad request
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/ErrorResponse'
          example:
            error:
              code: BAD_REQUEST
              message: invalid request body
    Unauthorized:
      description: Unauthorized
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/ErrorResponse'
          example:
            error:
              code: UNAUTHORIZED
              message: missing or invalid authorization header
    InternalServerError:
      description: Unexpected error
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/ErrorResponse'
          example:
            error:
              code: INTERNAL_ERROR
              message: internal error
  schemas:
    Wallet:
      type: object
    ErrorResponse:
      type: object
      properties:
        error:
          type: object
          properties:
            code:
              type: string
              enum: [BAD_REQUEST, UNAUTHORIZED, INTERNAL_ERROR]
            message:
              type: string
`

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func findingsFor(t *testing.T, doc string) []string {
	t.Helper()
	path := writeTemp(t, "openapi.yaml", doc)
	parsed, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out []string
	for _, f := range check(parsed).Findings {
		out = append(out, f.String())
	}
	return out
}

func TestGoodDocumentHasNoFindings(t *testing.T) {
	if got := findingsFor(t, goodDoc); len(got) != 0 {
		t.Fatalf("expected no findings, got:\n  %s", strings.Join(got, "\n  "))
	}
}

// TestEachRuleFires mutates one thing at a time. A rule that never fires is
// worse than no rule, because it reads as coverage.
func TestEachRuleFires(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantSub string
	}{
		{
			name:    "missing operationId",
			mutate:  func(d string) string { return strings.Replace(d, "      operationId: getWallet\n", "", 1) },
			wantSub: "missing operationId",
		},
		{
			name:    "duplicate operationId",
			mutate:  func(d string) string { return addPath(d, duplicatePath) },
			wantSub: "already used by",
		},
		{
			name:    "undeclared tag",
			mutate:  func(d string) string { return strings.Replace(d, "tags: [wallets]", "tags: [ghost]", 1) },
			wantSub: `tag "ghost" is not declared`,
		},
		{
			name:    "path parameter not declared",
			mutate:  func(d string) string { return strings.Replace(d, "/v1/wallets/{id}:", "/v1/wallets/{id}/{sub}:", 1) },
			wantSub: `path template parameter "sub" is not declared`,
		},
		{
			name:    "declared parameter absent from template",
			mutate:  func(d string) string { return strings.Replace(d, "/v1/wallets/{id}:", "/v1/wallets:", 1) },
			wantSub: "which the path template does not contain",
		},
		{
			name:    "no security requirement and no explicit opt-out",
			mutate:  func(d string) string { return removeGlobalSecurity(d) },
			wantSub: "add an explicit `security: []`",
		},
		{
			name: "unknown security scheme",
			mutate: func(d string) string {
				return strings.Replace(d, "security:\n  - BearerAuth: []", "security:\n  - MagicKey: []", 1)
			},
			wantSub: "is not defined in components.securitySchemes",
		},
		{
			name: "secured operation without 401",
			mutate: func(d string) string {
				return removeResponse(d, "        '401':\n          $ref: '#/components/responses/Unauthorized'\n")
			},
			wantSub: "does not document a 401 response",
		},
		{
			name: "admin route without AdminAuth",
			mutate: func(d string) string {
				return addPath(d, adminPath)
			},
			wantSub: "is an admin route but does not require the AdminAuth scheme",
		},
		{
			name: "no 5xx response",
			mutate: func(d string) string {
				return removeResponse(d, "        '500':\n          $ref: '#/components/responses/InternalServerError'\n")
			},
			wantSub: "documents no 5xx response",
		},
		{
			name: "no 4xx response",
			mutate: func(d string) string {
				// 401 is a 4xx too, so both have to go.
				d = removeResponse(d, "        '400':\n          $ref: '#/components/responses/BadRequest'\n")
				return removeResponse(d, "        '401':\n          $ref: '#/components/responses/Unauthorized'\n")
			},
			wantSub: "documents no 4xx response",
		},
		{
			name: "component response not using ErrorResponse",
			mutate: func(d string) string {
				return strings.Replace(d, "    BadRequest:\n      description: Bad request\n      content:\n        application/json:\n          schema:\n            $ref: '#/components/schemas/ErrorResponse'", "    BadRequest:\n      description: Bad request\n      content:\n        application/json:\n          schema:\n            type: object", 1)
			},
			wantSub: "components.responses.BadRequest: does not describe the ErrorResponse schema",
		},
		{
			name: "example with no error object",
			mutate: func(d string) string {
				return strings.Replace(d, "          example:\n            error:\n              code: UNAUTHORIZED\n              message: missing or invalid authorization header", "          example:\n            error: missing or invalid authorization header", 1)
			},
			wantSub: "example has no `error` object",
		},
		{
			name:    "example code outside the enum",
			mutate:  func(d string) string { return strings.Replace(d, "code: INTERNAL_ERROR", "code: TEAPOT", 1) },
			wantSub: `example uses error code "TEAPOT"`,
		},
		{
			name: "enum value not upper snake case",
			mutate: func(d string) string {
				return strings.Replace(d, "enum: [BAD_REQUEST, UNAUTHORIZED, INTERNAL_ERROR]", "enum: [BAD_REQUEST, Unauthorized_Token]", 1)
			},
			wantSub: `error code "Unauthorized_Token" is not UPPER_SNAKE_CASE`,
		},
		{
			name:    "idempotent route without the header",
			mutate:  func(d string) string { return addPath(d, idempotentPathWithoutHeader) },
			wantSub: "the Idempotency-Key header is not documented",
		},
		{
			name:    "idempotency header without uuid format",
			mutate:  func(d string) string { return addPath(d, idempotentPathBadFormat) },
			wantSub: "schema.format: uuid",
		},
		{
			name:    "idempotency header missing 409",
			mutate:  func(d string) string { return addPath(d, idempotentPathNo409) },
			wantSub: "does not document the 409",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			found := findingsFor(t, tc.mutate(goodDoc))
			for _, f := range found {
				if strings.Contains(f, tc.wantSub) {
					return
				}
			}
			t.Fatalf("expected a finding containing %q, got:\n  %s", tc.wantSub, strings.Join(found, "\n  "))
		})
	}
}

// addPath inserts a path entry into the document's paths block. Appending at the
// end of the file would nest it under components, which is not what these
// fixtures are testing.
func addPath(d, block string) string {
	i := strings.Index(d, "\ncomponents:")
	if i < 0 {
		panic("fixture document has no components block")
	}
	return d[:i] + "\n" + block + d[i:]
}

func removeGlobalSecurity(d string) string {
	return strings.Replace(d, "security:\n  - BearerAuth: []\n", "", 1)
}

func removeResponse(d, block string) string {
	return strings.Replace(d, block, "", 1)
}

const duplicatePath = `  /v1/wallets/{id}/copy:
    get:
      operationId: getWallet
      tags: [wallets]
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Wallet'
        '400':
          $ref: '#/components/responses/BadRequest'
        '500':
          $ref: '#/components/responses/InternalServerError'
`

const adminPath = `  /v1/admin/keys:
    get:
      operationId: listAdminKeys
      tags: [wallets]
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Wallet'
        '400':
          $ref: '#/components/responses/BadRequest'
        '500':
          $ref: '#/components/responses/InternalServerError'
`

const idempotentPathWithoutHeader = `  /v1/transfers:
    post:
      operationId: createTransfer
      tags: [wallets]
      responses:
        '202':
          description: accepted
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Wallet'
        '400':
          $ref: '#/components/responses/BadRequest'
        '500':
          $ref: '#/components/responses/InternalServerError'
`

const idempotentPathBadFormat = `  /v1/transfers:
    post:
      operationId: createTransfer
      tags: [wallets]
      parameters:
        - name: Idempotency-Key
          in: header
          required: true
          schema:
            type: string
      responses:
        '202':
          description: accepted
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Wallet'
        '400':
          $ref: '#/components/responses/BadRequest'
        '409':
          $ref: '#/components/responses/BadRequest'
        '422':
          $ref: '#/components/responses/BadRequest'
        '500':
          $ref: '#/components/responses/InternalServerError'
`

const idempotentPathNo409 = `  /v1/transfers:
    post:
      operationId: createTransfer
      tags: [wallets]
      parameters:
        - name: Idempotency-Key
          in: header
          required: true
          schema:
            type: string
            format: uuid
      responses:
        '202':
          description: accepted
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Wallet'
        '400':
          $ref: '#/components/responses/BadRequest'
        '422':
          $ref: '#/components/responses/BadRequest'
        '500':
          $ref: '#/components/responses/InternalServerError'
`

// TestRouteManifestDrift is the fixture the issue asks for: a manifest that
// promises a route the document does not define must fail loudly, because that
// is the shape a client hits when the spec and the server disagree.
func TestRouteManifestDrift(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "openapi.yaml")
	if err := os.WriteFile(specPath, []byte(goodDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, err := load(specPath)
	if err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(dir, "api-routes.yaml")
	// In sync with the document.
	if err := writeRouteManifest(manifestPath, doc); err != nil {
		t.Fatal(err)
	}
	if got := checkRouteManifest(manifestPath, doc); len(got) != 0 {
		t.Fatalf("a freshly written manifest should match, got: %v", got)
	}

	// Drift: the manifest claims a route the document never defines.
	drifted := manifestHeader + "\nroutes:\n  - \"GET /v1/wallets/{id}\"\n  - \"POST /v1/ghost\"\n"
	if err := os.WriteFile(manifestPath, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	got := checkRouteManifest(manifestPath, doc)
	if len(got) != 1 || !strings.Contains(got[0].Message, `route "POST /v1/ghost" is in the manifest but the OpenAPI document does not define it`) {
		t.Fatalf("expected one clear drift finding, got %v", got)
	}
	if !strings.HasPrefix(got[0].Location, manifestPath) {
		t.Fatalf("finding should be attributed to the manifest, got %q", got[0].Location)
	}

	// Drift the other way: the document gains an operation the manifest lacks.
	if err := writeRouteManifest(manifestPath, doc); err != nil {
		t.Fatal(err)
	}
	doc2, err := load(writeTemp(t, "extra.yaml", addPath(goodDoc, duplicatePath)))
	if err != nil {
		t.Fatal(err)
	}
	got = checkRouteManifest(manifestPath, doc2)
	if len(got) == 0 {
		t.Fatal("expected a finding for the operation missing from the manifest")
	}
	if !strings.Contains(got[0].Message, "missing from the manifest") {
		t.Fatalf("expected a missing-from-manifest finding, got %v", got)
	}
}

func TestWriteRouteManifestIsStable(t *testing.T) {
	path := writeTemp(t, "openapi.yaml", addPath(goodDoc, duplicatePath))
	doc, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(t.TempDir(), "a.yaml")
	second := filepath.Join(t.TempDir(), "b.yaml")
	if err := writeRouteManifest(first, doc); err != nil {
		t.Fatal(err)
	}
	if err := writeRouteManifest(second, doc); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(first)
	b, _ := os.ReadFile(second)
	if string(a) != string(b) {
		t.Fatal("regenerating the manifest twice produced different bytes")
	}
	if !strings.Contains(string(a), `"GET /v1/wallets/{id}"`) {
		t.Fatalf("manifest should list the operation id, got:\n%s", a)
	}
}
