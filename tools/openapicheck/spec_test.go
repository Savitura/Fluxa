package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two paths below are the files CI checks. Keeping them as constants means a
// test failure names the real artifact rather than a fixture.
const (
	repoSpec     = "../../docs/openapi.yaml"
	repoManifest = "../../docs/api-routes.yaml"
)

// TestRepoSpecHasNoFindings is the gate itself. The unit tests above prove each
// rule fires; this one proves the document in the repository satisfies all of
// them, so a careless edit to docs/openapi.yaml cannot merge.
func TestRepoSpecHasNoFindings(t *testing.T) {
	doc, err := load(repoSpec)
	if err != nil {
		t.Skipf("spec not available in this checkout: %v", err)
	}
	report := check(doc)
	if !report.empty() {
		t.Fatalf("%d problem(s) in %s:\n  %s", len(report.Findings), repoSpec, report.Findings)
	}
	if n := len(doc.operations()); n == 0 {
		t.Fatal("the document defines no operations, so every rule passed vacuously")
	}
}

// TestRepoManifestIsInSync fails when the checked-in manifest no longer matches
// what the document would generate.
func TestRepoManifestIsInSync(t *testing.T) {
	if _, err := os.Stat(repoSpec); err != nil {
		t.Skipf("spec not available in this checkout: %v", err)
	}
	doc, err := load(repoSpec)
	if err != nil {
		t.Fatal(err)
	}
	if got := checkRouteManifest(repoManifest, doc); len(got) != 0 {
		t.Fatalf("%s is out of sync with %s:\n  %s\nRun: go run ./tools/openapicheck -write-manifest", repoManifest, repoSpec, got)
	}
}

// TestServerErrorMayCarryItsOwnSchema pins the exemption that keeps the checker
// from forcing a meaningful 503 into the generic error envelope.
func TestServerErrorMayCarryItsOwnSchema(t *testing.T) {
	withDegraded := strings.Replace(goodDoc,
		"        \"500\":\n          $ref: '#/components/responses/InternalServerError'\n",
		"        \"500\":\n          $ref: '#/components/responses/InternalServerError'\n"+
			"        \"503\":\n"+
			"          description: \"Service degraded\"\n"+
			"          content:\n"+
			"            application/json:\n"+
			"              schema:\n"+
			"                $ref: '#/components/schemas/Wallet'\n"+
			"              example:\n"+
			"                status: \"degraded\"\n",
		1)
	found := findingsFor(t, withDegraded)
	for _, f := range found {
		if strings.Contains(f, "503") {
			t.Fatalf("a 503 with its own schema should be allowed, got: %s", f)
		}
	}
}

// TestEveryDocumentedExampleIsCovered keeps the examples honest against the enum
// in the repository document specifically, which is where the codes drift.
func TestEveryDocumentedExampleIsCovered(t *testing.T) {
	doc, err := load(repoSpec)
	if err != nil {
		t.Skipf("spec not available in this checkout: %v", err)
	}
	enum := doc.errorCodeEnum()
	if len(enum) == 0 {
		t.Fatal("the repository document has no error code enum")
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
			var r Report
			checkExample(doc, &r, op.ID(), resolved, enum)
			if !r.empty() {
				t.Fatalf("%s response %s: %s", op.ID(), code, r.Findings[0].Message)
			}
		}
	}
}

func TestLoadRejectsUnparseableFile(t *testing.T) {
	path := writeTemp(t, "bad.yaml", "openapi: 3.0.3\npaths:\n  - [oops\n")
	if _, err := load(path); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestManifestMustBeReadable(t *testing.T) {
	doc, err := load(writeTemp(t, "ok.yaml", goodDoc))
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	if got := checkRouteManifest(missing, doc); len(got) != 1 {
		t.Fatalf("expected one finding for an unreadable manifest, got %v", got)
	}
}
