package compliance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

func loadFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/sdn_sample.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

func findEntity(entities []*domain.SanctionsEntity, uid, address string) *domain.SanctionsEntity {
	for _, e := range entities {
		if e.UID == uid && e.Address == address {
			return e
		}
	}
	return nil
}

func TestParseSDNExtractsDigitalCurrencyAddresses(t *testing.T) {
	entities, err := ParseSDN(strings.NewReader(loadFixture(t)))
	if err != nil {
		t.Fatalf("ParseSDN: %v", err)
	}

	xlm := findEntity(entities, "10001", "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABLOK")
	if xlm == nil {
		t.Fatal("did not extract the XLM address for uid 10001")
	}
	if xlm.Name != "Ayman Zawahiri" {
		t.Fatalf("name = %q, want %q", xlm.Name, "Ayman Zawahiri")
	}
	if xlm.EntityType != "Individual" {
		t.Fatalf("entity type = %q, want Individual", xlm.EntityType)
	}
	if xlm.Source != SourceOFAC {
		t.Fatalf("source = %q, want %q", xlm.Source, SourceOFAC)
	}
	if len(xlm.Programs) != 1 || xlm.Programs[0] != "SDGT" {
		t.Fatalf("programs = %v, want [SDGT]", xlm.Programs)
	}
}

// One entry can carry several addresses across different chains; all of them
// are indexed, not just the Stellar one.
func TestParseSDNExtractsMultipleAddressesPerEntry(t *testing.T) {
	entities, err := ParseSDN(strings.NewReader(loadFixture(t)))
	if err != nil {
		t.Fatalf("ParseSDN: %v", err)
	}

	if findEntity(entities, "10002", "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2") == nil {
		t.Fatal("did not extract the XBT address for uid 10002")
	}
	if findEntity(entities, "10002", "GBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBDPRK") == nil {
		t.Fatal("did not extract the XLM address for uid 10002")
	}
}

// A Passport id is not a wallet address and must not become one.
func TestParseSDNIgnoresNonCurrencyIdentifiers(t *testing.T) {
	entities, err := ParseSDN(strings.NewReader(loadFixture(t)))
	if err != nil {
		t.Fatalf("ParseSDN: %v", err)
	}
	for _, e := range entities {
		if e.Address == "X9911223" {
			t.Fatal("passport number was indexed as a digital currency address")
		}
	}
}

func TestParseSDNIndexesNamesAndAliases(t *testing.T) {
	entities, err := ParseSDN(strings.NewReader(loadFixture(t)))
	if err != nil {
		t.Fatalf("ParseSDN: %v", err)
	}

	names := map[string]bool{}
	for _, e := range entities {
		names[e.Name] = true
	}

	for _, want := range []string{
		"Ayman Zawahiri",
		"Aiman Zawahri",
		"NORTH KOREA MINING TRADING CORP",
		"Maria Sanchez Delgado",
		"QUIET SHIPPING LLC",
	} {
		if !names[want] {
			t.Fatalf("name %q missing from parsed entities", want)
		}
	}
}

// An entry with no address at all still has to be screenable by name.
func TestParseSDNHandlesEntryWithoutIdentifiers(t *testing.T) {
	entities, err := ParseSDN(strings.NewReader(loadFixture(t)))
	if err != nil {
		t.Fatalf("ParseSDN: %v", err)
	}
	if findEntity(entities, "10004", "") == nil {
		t.Fatal("uid 10004 (no idList) produced no name-only entity")
	}
}

func TestParseSDNRejectsMalformedXML(t *testing.T) {
	_, err := ParseSDN(strings.NewReader(`<sdnList><sdnEntry><uid>1</uid>`))
	if err == nil {
		t.Fatal("expected an error for truncated XML, got nil")
	}
}

func TestParseSDNOnEmptyListReturnsNoEntities(t *testing.T) {
	entities, err := ParseSDN(strings.NewReader(`<?xml version="1.0"?><sdnList></sdnList>`))
	if err != nil {
		t.Fatalf("ParseSDN: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("got %d entities, want 0", len(entities))
	}
}

func TestHTTPSDNSourceFetchesFromServer(t *testing.T) {
	fixture := loadFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	body, err := NewHTTPSDNSource(srv.URL, srv.Client()).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer body.Close()

	entities, err := ParseSDN(body)
	if err != nil {
		t.Fatalf("ParseSDN: %v", err)
	}
	if len(entities) == 0 {
		t.Fatal("parsed zero entities from the served fixture")
	}
}

func TestHTTPSDNSourceRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := NewHTTPSDNSource(srv.URL, srv.Client()).Fetch(context.Background()); err == nil {
		t.Fatal("expected an error for a 503 response, got nil")
	}
}

func TestHTTPSDNSourceRequiresAURL(t *testing.T) {
	if _, err := NewHTTPSDNSource("", nil).Fetch(context.Background()); err == nil {
		t.Fatal("expected an error when OFAC_SDN_URL is unset, got nil")
	}
}

// The acceptance criterion is that a refresh parses and rebuilds well inside
// 60 seconds; against the fixture it is effectively instant, so this asserts
// the whole path completes and records its audit row.
func TestWorkerRefreshParsesPersistsAndRecordsAudit(t *testing.T) {
	fixture := loadFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	repo := newComplianceMockRepo()
	set := NewSanctionsSet()
	hooks := &mockDispatcher{}
	worker := NewWorker(repo, NewHTTPSDNSource(srv.URL, srv.Client()), set, hooks)

	started := time.Now()
	if err := worker.HandleRefreshSanctions(context.Background(), nil); err != nil {
		t.Fatalf("HandleRefreshSanctions: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 60*time.Second {
		t.Fatalf("refresh took %s, want under 60s", elapsed)
	}

	if len(repo.updates) != 1 {
		t.Fatalf("recorded %d sanctions updates, want 1", len(repo.updates))
	}
	update := repo.updates[0]
	if update.Status != domain.SanctionsUpdateSuccess {
		t.Fatalf("update status = %q, want success", update.Status)
	}
	if update.EntityCount == 0 {
		t.Fatal("update recorded a zero entity count")
	}

	// The in-memory set must be usable immediately, without waiting a tick.
	if !set.Loaded() {
		t.Fatal("sanctions set was not loaded after a successful refresh")
	}
	if _, ok := set.MatchAddress("GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABLOK"); !ok {
		t.Fatal("refreshed set does not match the fixture's sanctioned address")
	}
}

func TestWorkerRefreshRecordsFailureAndDispatchesWebhook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	repo := newComplianceMockRepo()
	hooks := &mockDispatcher{}
	worker := NewWorker(repo, NewHTTPSDNSource(srv.URL, srv.Client()), NewSanctionsSet(), hooks)

	if err := worker.HandleRefreshSanctions(context.Background(), nil); err == nil {
		t.Fatal("expected a failed refresh to return an error so asynq retries")
	}

	if len(repo.updates) != 1 || repo.updates[0].Status != domain.SanctionsUpdateFailed {
		t.Fatalf("expected one failed audit row, got %+v", repo.updates)
	}
	if repo.updates[0].Error == "" {
		t.Fatal("failed audit row recorded no error text")
	}
	if !hooks.dispatched(domain.EventSanctionsRefreshFailed) {
		t.Fatalf("expected a %s webhook, got %v", domain.EventSanctionsRefreshFailed, hooks.events)
	}
}

// A malformed download that parses to nothing must not wipe the existing
// list — an empty set would clear every subsequent screen.
func TestWorkerRefreshRefusesToApplyAnEmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><sdnList></sdnList>`))
	}))
	defer srv.Close()

	repo := newComplianceMockRepo()
	set := sanctionsSetFixture(t)
	worker := NewWorker(repo, NewHTTPSDNSource(srv.URL, srv.Client()), set, &mockDispatcher{})

	if err := worker.HandleRefreshSanctions(context.Background(), nil); err == nil {
		t.Fatal("expected an error when the parsed list is empty")
	}
	if repo.replaceCalls != 0 {
		t.Fatalf("empty list was written to the database (%d calls)", repo.replaceCalls)
	}
	if _, ok := set.MatchAddress(sanctionedAddr); !ok {
		t.Fatal("previous sanctions set was discarded by a failed refresh")
	}
}

func TestSanctionsSetLoadFromRepositoryPropagatesErrors(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.listErr = errors.New("database unreachable")

	if err := NewSanctionsSet().LoadFromRepository(context.Background(), repo); err == nil {
		t.Fatal("expected the load error to surface")
	}
}
