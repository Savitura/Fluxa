// Command openapicheck validates the Fluxa OpenAPI document.
//
// The document at docs/openapi.yaml is the published contract for the API and is
// served verbatim from /docs/openapi.yaml, so a mistake in it is a mistake in the
// product's documentation. This tool is the gate that keeps it honest: it parses
// the document and reports every way in which it can drift from the server's
// actual behaviour.
//
// It lives in the root module but deliberately depends on nothing but
// gopkg.in/yaml.v3, so `go run ./tools/openapicheck` still works while other
// packages in the module do not compile (see issue #186). A check that could not
// run would not be a gate.
//
// Usage:
//
//	openapicheck -spec docs/openapi.yaml -manifest docs/api-routes.yaml
//	openapicheck -spec docs/openapi.yaml -write-manifest
//
// It exits 0 when the document satisfies every rule and 1 otherwise, printing one
// "location: message" line per finding.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// idempotentRoutes are the routes whose handlers attach the idempotency-key
// middleware via WithIdempotency, and which therefore must document the header
// and every response the middleware can produce.
//
// Source of truth: internal/transfer/handler.go (WithIdempotency wraps POST /)
// and internal/batch/handler.go (WithIdempotency wraps the batch POST). When the
// wiring moves, this list moves with it.
var idempotentRoutes = []string{
	"POST /v1/transfers",
	"POST /v1/transfers/batch",
}

// idempotencyHeader is the header internal/server/idempotency reads. The
// middleware answers 400 when it is missing or not a UUID v4, 409 while a request
// with the same key is still in flight, and 422 when the key is reused with a
// different body.
const idempotencyHeader = "Idempotency-Key"

// Finding is one problem with the document.
type Finding struct {
	Location string
	Message  string
}

func (f Finding) String() string { return f.Location + ": " + f.Message }

// Report is an ordered set of findings.
type Report struct {
	Findings []Finding
}

func (r *Report) add(location, format string, args ...interface{}) {
	r.Findings = append(r.Findings, Finding{Location: location, Message: fmt.Sprintf(format, args...)})
}

func (r *Report) empty() bool { return len(r.Findings) == 0 }

// Err is returned when the document has findings.
type Err struct{ Report Report }

func (e *Err) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "openapi document has %d problem(s):", len(e.Report.Findings))
	for _, f := range e.Report.Findings {
		fmt.Fprintf(&b, "\n  %s", f)
	}
	return b.String()
}

func main() {
	specPath := flag.String("spec", "docs/openapi.yaml", "path to the OpenAPI document")
	manifestPath := flag.String("manifest", "docs/api-routes.yaml", "path to the generated route manifest")
	writeManifest := flag.Bool("write-manifest", false, "regenerate the route manifest from the document and exit")
	flag.Parse()

	doc, err := load(*specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapicheck: %v\n", err)
		os.Exit(2)
	}

	if *writeManifest {
		if err := writeRouteManifest(*manifestPath, doc); err != nil {
			fmt.Fprintf(os.Stderr, "openapicheck: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf("wrote %s (%d operations)\n", *manifestPath, len(doc.operations()))
		return
	}

	report := check(doc)

	if *manifestPath != "" {
		report.Findings = append(report.Findings, checkRouteManifest(*manifestPath, doc)...)
	}

	if !report.empty() {
		for _, f := range report.Findings {
			fmt.Fprintln(os.Stderr, f)
		}
		fmt.Fprintf(os.Stderr, "\n%d problem(s) found.\n", len(report.Findings))
		os.Exit(1)
	}
	fmt.Printf("openapi: %d operations, no problems found\n", len(doc.operations()))
}

// ── the document ────────────────────────────────────────────────────────────

// Document is a parsed OpenAPI file. It is kept as generic YAML rather than a
// full model: the rules below are about the shape of the document, and a model
// would hide exactly the malformed input they need to see.
type Document struct {
	Root map[string]interface{}
}

func load(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if root == nil {
		return nil, fmt.Errorf("%s is empty", path)
	}
	return &Document{Root: root}, nil
}

// Operation is one method under one path.
type Operation struct {
	Method string
	Path   string
	Node   map[string]interface{}
}

// ID is the "METHOD /path" form used in findings and in the route manifest.
func (o Operation) ID() string { return o.Method + " " + o.Path }

var methods = []string{"get", "put", "post", "delete", "patch", "options", "head", "trace"}

func (d *Document) operations() []Operation {
	paths := d.paths()
	var ops []Operation
	for _, path := range sortedKeys(paths) {
		item, ok := paths[path].(map[string]interface{})
		if !ok {
			continue
		}
		for _, method := range methods {
			node, ok := item[method].(map[string]interface{})
			if !ok {
				continue
			}
			ops = append(ops, Operation{Method: strings.ToUpper(method), Path: path, Node: node})
		}
	}
	return ops
}

func (d *Document) paths() map[string]interface{} {
	p, _ := d.Root["paths"].(map[string]interface{})
	return p
}

func (d *Document) components() map[string]interface{} {
	c, _ := d.Root["components"].(map[string]interface{})
	return c
}

func (d *Document) globalSecurity() []interface{} {
	s, _ := d.Root["security"].([]interface{})
	return s
}

func (d *Document) declaredTags() map[string]bool {
	tags := map[string]bool{}
	list, _ := d.Root["tags"].([]interface{})
	for _, t := range list {
		if m, ok := t.(map[string]interface{}); ok {
			if name, ok := m["name"].(string); ok {
				tags[name] = true
			}
		}
	}
	return tags
}

func (d *Document) securitySchemes() map[string]interface{} {
	c, _ := d.components()["securitySchemes"].(map[string]interface{})
	return c
}

func (d *Document) errorCodeEnum() map[string]bool {
	codes := map[string]bool{}
	schema, ok := d.schema("ErrorResponse")
	if !ok {
		return codes
	}
	errObj, _ := schema["properties"].(map[string]interface{})
	errorProp, _ := errObj["error"].(map[string]interface{})
	props, _ := errorProp["properties"].(map[string]interface{})
	codeProp, _ := props["code"].(map[string]interface{})
	list, _ := codeProp["enum"].([]interface{})
	for _, c := range list {
		if s, ok := c.(string); ok {
			codes[s] = true
		}
	}
	return codes
}

func (d *Document) schema(name string) (map[string]interface{}, bool) {
	schemas, _ := d.components()["schemas"].(map[string]interface{})
	s, ok := schemas[name].(map[string]interface{})
	return s, ok
}

// resolve follows a local $ref one hop, which is all this document uses.
func (d *Document) resolve(node map[string]interface{}) (map[string]interface{}, string) {
	ref, _ := node["$ref"].(string)
	if !strings.HasPrefix(ref, "#/components/") {
		return node, ""
	}
	parts := strings.Split(strings.TrimPrefix(ref, "#/components/"), "/")
	var current interface{} = d.components()
	for _, p := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return node, ref
		}
		current, ok = m[p]
		if !ok {
			return node, ref
		}
	}
	resolved, ok := current.(map[string]interface{})
	if !ok {
		return node, ref
	}
	return resolved, ref
}

// ── rules ───────────────────────────────────────────────────────────────────

func check(doc *Document) Report {
	var r Report
	checkDocumentShape(doc, &r)
	checkOperationIDs(doc, &r)
	checkTags(doc, &r)
	checkPathParameters(doc, &r)
	checkSecurity(doc, &r)
	checkErrorResponses(doc, &r)
	checkErrorExamples(doc, &r)
	checkErrorCodeEnum(doc, &r)
	checkIdempotency(doc, &r)
	return r
}

func checkDocumentShape(doc *Document, r *Report) {
	version, _ := doc.Root["openapi"].(string)
	if !strings.HasPrefix(version, "3.0") {
		r.add("openapi", "version is %q, want a 3.0.x document", version)
	}
	info, ok := doc.Root["info"].(map[string]interface{})
	if !ok {
		r.add("info", "missing")
		return
	}
	for _, field := range []string{"title", "version"} {
		if v, _ := info[field].(string); strings.TrimSpace(v) == "" {
			r.add("info."+field, "missing")
		}
	}
	if len(doc.paths()) == 0 {
		r.add("paths", "no paths documented")
	}
	if len(doc.components()) == 0 {
		r.add("components", "no components documented")
	}
}

func checkOperationIDs(doc *Document, r *Report) {
	seen := map[string]string{}
	for _, op := range doc.operations() {
		id, _ := op.Node["operationId"].(string)
		if strings.TrimSpace(id) == "" {
			r.add(op.ID(), "missing operationId")
			continue
		}
		if prev, dup := seen[id]; dup {
			r.add(op.ID(), "operationId %q is already used by %s", id, prev)
			continue
		}
		seen[id] = op.ID()
	}
}

func checkTags(doc *Document, r *Report) {
	declared := doc.declaredTags()
	for _, op := range doc.operations() {
		tags, _ := op.Node["tags"].([]interface{})
		if len(tags) == 0 {
			r.add(op.ID(), "no tags")
			continue
		}
		for _, t := range tags {
			name, _ := t.(string)
			if !declared[name] {
				r.add(op.ID(), "tag %q is not declared in the top-level tags list", name)
			}
		}
	}
}

// checkPathParameters catches a path template that promises a parameter the
// operation never declares: the single most common way a documented URL and its
// real signature drift apart.
func checkPathParameters(doc *Document, r *Report) {
	for _, op := range doc.operations() {
		inTemplate := map[string]bool{}
		for _, seg := range strings.Split(op.Path, "/") {
			if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
				inTemplate[seg[1:len(seg)-1]] = true
			}
		}

		declared := map[string]bool{}
		collect := func(list []interface{}) {
			for _, p := range list {
				pm, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				resolved, _ := doc.resolve(pm)
				name, _ := resolved["name"].(string)
				in, _ := resolved["in"].(string)
				if in == "path" {
					declared[name] = true
				}
			}
		}
		collect(parametersOf(doc, op))

		for name := range inTemplate {
			if !declared[name] {
				r.add(op.ID(), "path template parameter %q is not declared as an in:path parameter", name)
			}
		}
		for name := range declared {
			if !inTemplate[name] {
				r.add(op.ID(), "declares in:path parameter %q, which the path template does not contain", name)
			}
		}
	}
}

// parametersOf merges the path-level and operation-level parameter lists.
func parametersOf(doc *Document, op Operation) []interface{} {
	var out []interface{}
	paths := doc.paths()
	if item, ok := paths[op.Path].(map[string]interface{}); ok {
		if list, ok := item["parameters"].([]interface{}); ok {
			out = append(out, list...)
		}
	}
	if list, ok := op.Node["parameters"].([]interface{}); ok {
		out = append(out, list...)
	}
	return out
}

func checkSecurity(doc *Document, r *Report) {
	schemes := doc.securitySchemes()
	global := doc.globalSecurity()

	for _, op := range doc.operations() {
		explicit, hasExplicit := op.Node["security"]
		effective := global
		if hasExplicit {
			list, _ := explicit.([]interface{})
			effective = list
		}

		if len(effective) == 0 {
			// An operation with no security is fine, but only if it says so.
			// Relying on the absence of a global default is how an endpoint
			// becomes public by accident.
			if !hasExplicit {
				r.add(op.ID(), "documents no security requirement; add an explicit `security: []` if it is public")
			}
			continue
		}

		secured := false
		for _, req := range effective {
			m, ok := req.(map[string]interface{})
			if !ok {
				continue
			}
			for name := range m {
				if _, known := schemes[name]; !known {
					r.add(op.ID(), "security scheme %q is not defined in components.securitySchemes", name)
					continue
				}
				secured = true
			}
		}

		if secured && !hasResponse(op, "401") {
			r.add(op.ID(), "requires authentication but does not document a 401 response")
		}
		if strings.HasPrefix(op.Path, "/v1/admin/") && !requiresScheme(effective, "AdminAuth") {
			r.add(op.ID(), "is an admin route but does not require the AdminAuth scheme")
		}
	}
}

func requiresScheme(security []interface{}, scheme string) bool {
	for _, req := range security {
		m, ok := req.(map[string]interface{})
		if !ok {
			continue
		}
		if _, ok := m[scheme]; ok {
			return true
		}
	}
	return false
}

// checkErrorResponses enforces that every operation can fail in a way a client
// can act on, and that the failures are described with the shared error schema
// rather than an ad-hoc one.
func checkErrorResponses(doc *Document, r *Report) {
	responses, _ := doc.components()["responses"].(map[string]interface{})
	for _, name := range sortedKeys(responses) {
		node, _ := responses[name].(map[string]interface{})
		resolved, _ := doc.resolve(node)
		if !referencesErrorResponse(doc, resolved) {
			r.add("components.responses."+name, "does not describe the ErrorResponse schema")
		}
	}

	for _, op := range doc.operations() {
		codes := responseCodes(op)
		client, server := 0, 0
		for _, code := range codes {
			switch {
			case strings.HasPrefix(code, "4"):
				client++
			case strings.HasPrefix(code, "5"):
				server++
			}
		}
		if client == 0 {
			r.add(op.ID(), "documents no 4xx response")
		}
		if server == 0 {
			r.add(op.ID(), "documents no 5xx response")
		}
		for _, code := range codes {
			if !usesErrorSchema(code) {
				continue
			}
			node := responseNode(op, code)
			resolved, _ := doc.resolve(node)
			if !referencesErrorResponse(doc, resolved) {
				r.add(op.ID(), "response %s does not describe the ErrorResponse schema", code)
			}
		}
	}
}

// usesErrorSchema reports whether a status code is expected to carry the shared
// error envelope. Every client error is one. Among server errors only 500 is:
// a 503 that reports degraded health legitimately describes its own body.
func usesErrorSchema(code string) bool {
	return strings.HasPrefix(code, "4") || code == "500"
}

// referencesErrorResponse reports whether a response body is the shared
// ErrorResponse schema, following a $ref if that is how it is written.
func referencesErrorResponse(doc *Document, response map[string]interface{}) bool {
	content, _ := response["content"].(map[string]interface{})
	json, _ := content["application/json"].(map[string]interface{})
	schema, _ := json["schema"].(map[string]interface{})
	if schema == nil {
		return false
	}
	resolved, _ := doc.resolve(schema)
	if ref, _ := schema["$ref"].(string); ref == "#/components/schemas/ErrorResponse" {
		return true
	}
	// A copy of the schema is acceptable as long as it carries the code and
	// message pair a client parses.
	errProp, _ := resolved["properties"].(map[string]interface{})
	errorObj, _ := errProp["error"].(map[string]interface{})
	props, _ := errorObj["properties"].(map[string]interface{})
	_, hasCode := props["code"]
	_, hasMessage := props["message"]
	return hasCode && hasMessage
}

// checkErrorExamples keeps the examples honest. An example that disagrees with the
// schema is worse than no example, because a client developer trusts it.
func checkErrorExamples(doc *Document, r *Report) {
	codes := doc.errorCodeEnum()
	if len(codes) == 0 {
		r.add("components.schemas.ErrorResponse", "error.code has no enum, so examples cannot be checked against it")
		return
	}

	responses, _ := doc.components()["responses"].(map[string]interface{})
	for _, name := range sortedKeys(responses) {
		node, _ := responses[name].(map[string]interface{})
		checkExample(doc, r, "components.responses."+name, node, codes)
	}

	for _, op := range doc.operations() {
		for _, code := range responseCodes(op) {
			if !usesErrorSchema(code) {
				continue
			}
			resolved, _ := doc.resolve(responseNode(op, code))
			if !referencesErrorResponse(doc, resolved) {
				continue
			}
			checkExample(doc, r, op.ID()+" response "+code, responseNode(op, code), codes)
		}
	}
}

func checkExample(doc *Document, r *Report, location string, response map[string]interface{}, codes map[string]bool) {
	resolved, _ := doc.resolve(response)
	content, _ := resolved["content"].(map[string]interface{})
	media, _ := content["application/json"].(map[string]interface{})
	example, ok := media["example"].(map[string]interface{})
	if !ok {
		if media != nil {
			if _, has := media["example"]; has {
				r.add(location, "example is not a JSON object")
			}
		}
		return
	}
	errObj, ok := example["error"].(map[string]interface{})
	if !ok {
		r.add(location, "example has no `error` object, so it does not match ErrorResponse")
		return
	}
	code, _ := errObj["code"].(string)
	if code == "" {
		r.add(location, "example error has no `code`")
		return
	}
	if _, known := codes[code]; !known {
		r.add(location, "example uses error code %q, which is not in the ErrorResponse code enum", code)
	}
}

// checkErrorCodeEnum guards the enum's own shape, so a typo there is caught
// before a client copies it.
func checkErrorCodeEnum(doc *Document, r *Report) {
	for code := range doc.errorCodeEnum() {
		if code != strings.ToUpper(code) || strings.ContainsAny(code, " -") {
			r.add("components.schemas.ErrorResponse", "error code %q is not UPPER_SNAKE_CASE", code)
		}
	}
}

// checkIdempotency ties the documented contract to the middleware that enforces
// it. The middleware can answer 400, 409 and 422 on its own; an operation that
// uses it and does not document those leaves a client guessing.
func checkIdempotency(doc *Document, r *Report) {
	required := map[string]bool{}
	for _, id := range idempotentRoutes {
		required[id] = true
	}

	for _, op := range doc.operations() {
		param := findIdempotencyParam(doc, op)
		if param == nil {
			if required[op.ID()] {
				r.add(op.ID(), "the handler attaches the idempotency middleware but the %s header is not documented", idempotencyHeader)
				for _, code := range []string{"400", "409", "422"} {
					if !hasResponse(op, code) {
						r.add(op.ID(), "does not document the %s the idempotency middleware can return", code)
					}
				}
			}
			continue
		}
		if in, _ := param["in"].(string); in != "header" {
			r.add(op.ID(), "%s must be declared with in: header, got %q", idempotencyHeader, in)
		}
		schema, _ := param["schema"].(map[string]interface{})
		if format, _ := schema["format"].(string); format != "uuid" {
			r.add(op.ID(), "%s must declare schema.format: uuid — the middleware rejects anything else with 400", idempotencyHeader)
		}
		for _, code := range []string{"400", "409", "422"} {
			if !hasResponse(op, code) {
				r.add(op.ID(), "declares %s but does not document the %s the middleware can return", idempotencyHeader, code)
			}
		}
	}
}

func findIdempotencyParam(doc *Document, op Operation) map[string]interface{} {
	for _, p := range parametersOf(doc, op) {
		pm, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		resolved, _ := doc.resolve(pm)
		if name, _ := resolved["name"].(string); name == idempotencyHeader {
			return resolved
		}
	}
	return nil
}

func responseCodes(op Operation) []string {
	responses, _ := op.Node["responses"].(map[string]interface{})
	return sortedKeys(responses)
}

func responseNode(op Operation, code string) map[string]interface{} {
	responses, _ := op.Node["responses"].(map[string]interface{})
	node, _ := responses[code].(map[string]interface{})
	return node
}

func hasResponse(op Operation, code string) bool {
	responses, _ := op.Node["responses"].(map[string]interface{})
	_, ok := responses[code]
	return ok
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
