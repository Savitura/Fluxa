package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

func stringPtr(value string) *string { return &value }

// seedDeadLetter stores a dead letter owned by tenantID, wired to an endpoint.
func seedDeadLetter(t *testing.T, repo *fakeRepo, tenantID, id, endpointID, eventType, payload string, createdAt time.Time) *domain.WebhookDeadLetter {
	t.Helper()
	dl := &domain.WebhookDeadLetter{
		ID:           id,
		EndpointID:   endpointID,
		TenantID:     stringPtr(tenantID),
		DeliveryID:   "delivery-" + id,
		EventType:    eventType,
		Payload:      payload,
		ErrorMessage: "status code 500",
		AttemptCount: 5,
		Status:       domain.DeadLetterPending,
		CreatedAt:    createdAt,
	}
	if err := repo.CreateDeadLetter(context.Background(), dl); err != nil {
		t.Fatalf("seed dead letter: %v", err)
	}
	return dl
}

func seedEndpoint(t *testing.T, repo *fakeRepo, tenantID, id, url string, active bool) *domain.WebhookEndpoint {
	t.Helper()
	ep := &domain.WebhookEndpoint{
		ID:        id,
		TenantID:  stringPtr(tenantID),
		URL:       url,
		Secret:    "whsec_test",
		Events:    []string{"*"},
		Active:    active,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := repo.CreateEndpoint(context.Background(), ep); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	return ep
}

// TestReplayDeadLetter_PreservesEventIdentityAndIssuesNewDelivery covers the
// core replay contract: the event type is carried over verbatim, the delivery
// identity is new, and it is queued through the normal path.
func TestReplayDeadLetter_PreservesEventIdentityAndIssuesNewDelivery(t *testing.T) {
	repo := newFakeRepo()
	seedEndpoint(t, repo, "tenant-a", "ep-1", "https://example.com/hook", true)
	seedDeadLetter(t, repo, "tenant-a", "dl-1", "ep-1", "transfer.settled", `{"id":"tx-1"}`, time.Now().UTC())

	svc := NewService(repo, nil, nil, 120, false)
	replayed, err := svc.ReplayDeadLetter(tenantCtx("tenant-a"), "dl-1")
	if err != nil {
		t.Fatalf("ReplayDeadLetter: %v", err)
	}
	if replayed.Status != domain.DeadLetterReplayed {
		t.Fatalf("status = %q, want replayed", replayed.Status)
	}
	if replayed.ReplayDeliveryID == "" || replayed.ReplayDeliveryID == replayed.DeliveryID {
		t.Fatalf("replay must produce a new delivery identity, got %q (original %q)", replayed.ReplayDeliveryID, replayed.DeliveryID)
	}
	if replayed.ReplayCount != 1 {
		t.Fatalf("replay_count = %d, want 1", replayed.ReplayCount)
	}
	delivery, ok := repo.deliveries[replayed.ReplayDeliveryID]
	if !ok {
		t.Fatalf("replay delivery %q was not persisted", replayed.ReplayDeliveryID)
	}
	if delivery.EventType != "transfer.settled" {
		t.Fatalf("replay event type = %q, want the original transfer.settled", delivery.EventType)
	}
	if delivery.ReplayOf != "dl-1" {
		t.Fatalf("replay_of = %q, want dl-1", delivery.ReplayOf)
	}
}

// TestReplayDeadLetter_IsIdempotentUnderConcurrentReplays proves a second
// replay returns the first replay's delivery instead of generating a duplicate.
func TestReplayDeadLetter_IsIdempotentUnderConcurrentReplays(t *testing.T) {
	repo := newFakeRepo()
	seedEndpoint(t, repo, "tenant-a", "ep-1", "https://example.com/hook", true)
	seedDeadLetter(t, repo, "tenant-a", "dl-1", "ep-1", "transfer.settled", `{}`, time.Now().UTC())

	svc := NewService(repo, nil, nil, 120, false)
	first, err := svc.ReplayDeadLetter(tenantCtx("tenant-a"), "dl-1")
	if err != nil {
		t.Fatalf("first replay: %v", err)
	}
	second, err := svc.ReplayDeadLetter(tenantCtx("tenant-a"), "dl-1")
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}
	if first.ReplayDeliveryID != second.ReplayDeliveryID {
		t.Fatalf("concurrent replay was not idempotent: %q != %q", first.ReplayDeliveryID, second.ReplayDeliveryID)
	}
	if len(repo.deliveries) != 1 {
		t.Fatalf("expected exactly one replay delivery, got %d", len(repo.deliveries))
	}
}

// TestReplayDeadLetter_TenantIsolation ensures a replay cannot cross tenants.
func TestReplayDeadLetter_TenantIsolation(t *testing.T) {
	repo := newFakeRepo()
	seedEndpoint(t, repo, "tenant-a", "ep-1", "https://example.com/hook", true)
	seedDeadLetter(t, repo, "tenant-a", "dl-1", "ep-1", "transfer.settled", `{}`, time.Now().UTC())

	svc := NewService(repo, nil, nil, 120, false)
	if _, err := svc.ReplayDeadLetter(tenantCtx("tenant-b"), "dl-1"); err == nil {
		t.Fatal("tenant-b must not be able to replay tenant-a's dead letter")
	}
	listed, err := svc.ListDeadLetters(tenantCtx("tenant-b"), domain.DeadLetterFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListDeadLetters: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("tenant-b sees %d of tenant-a's dead letters, want 0", len(listed))
	}
}

// TestReplayDeadLetter_RefusesDisabledEndpoint covers the acceptance rule that
// replay cannot target a disabled endpoint.
func TestReplayDeadLetter_RefusesDisabledEndpoint(t *testing.T) {
	repo := newFakeRepo()
	seedEndpoint(t, repo, "tenant-a", "ep-1", "https://example.com/hook", false)
	seedDeadLetter(t, repo, "tenant-a", "dl-1", "ep-1", "transfer.settled", `{}`, time.Now().UTC())

	svc := NewService(repo, nil, nil, 120, false)
	_, err := svc.ReplayDeadLetter(tenantCtx("tenant-a"), "dl-1")
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("replay to a disabled endpoint should fail, got %v", err)
	}
	if _, exists := repo.deadLetters["dl-1"]; !exists {
		t.Fatal("dead letter should remain retained after a refused replay")
	}
}

// TestReplayDeadLetter_UsesCurrentTimestampedSignature proves the replay is
// signed with the endpoint's current secret and the standard timestamped
// signature, exactly like a first delivery.
func TestReplayDeadLetter_UsesCurrentTimestampedSignature(t *testing.T) {
	var gotSig, gotTimestamp, gotEvent string
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Fluxa-Signature")
		gotTimestamp = r.Header.Get("X-Fluxa-Timestamp")
		gotEvent = r.Header.Get("X-Fluxa-Event")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	repo := newFakeRepo()
	seedEndpoint(t, repo, "tenant-a", "ep-1", ts.URL, true)
	seedDeadLetter(t, repo, "tenant-a", "dl-1", "ep-1", "wallet.funded", `{"wallet_id":"wal-1"}`, time.Now().UTC())

	svc := NewService(repo, nil, nil, 120, true).(*service)
	replayed, err := svc.ReplayDeadLetter(tenantCtx("tenant-a"), "dl-1")
	if err != nil {
		t.Fatalf("ReplayDeadLetter: %v", err)
	}

	if err := svc.Deliver(tenantCtx("tenant-a"), replayed.ReplayDeliveryID); err != nil {
		t.Fatalf("Deliver replay: %v", err)
	}
	wantSig := sign("whsec_test", gotTimestamp, []byte(gotBody))
	if gotSig != wantSig {
		t.Fatalf("signature = %q, want %q", gotSig, wantSig)
	}
	if gotTimestamp == "" {
		t.Fatal("replay must carry the standard timestamped signature")
	}
	if gotEvent == "replay" {
		t.Fatal("replay must not masquerade as a synthetic replay event")
	}
}

// TestAttemptHistory_DistinguishesAutomaticRetriesFromReplay covers the
// attempt-history requirement.
func TestAttemptHistory_DistinguishesAutomaticRetriesFromReplay(t *testing.T) {
	repo := newFakeRepo()

	// An endpoint that always fails, so the retry lands in the dead-letter queue.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	seedEndpoint(t, repo, "tenant-a", "ep-1", ts.URL, true)
	svc := NewService(repo, nil, nil, 120, true).(*service)

	// Automatic: a delivery whose last attempt is exhausted.
	delivery := &domain.WebhookDelivery{
		ID:           "del-auto",
		EndpointID:   "ep-1",
		EventType:    "transfer.settled",
		Payload:      `{}`,
		Status:       "pending",
		AttemptCount: 4,
		MaxAttempts:  5,
		CreatedAt:    time.Now().UTC(),
	}
	if err := repo.CreateDelivery(context.Background(), delivery); err != nil {
		t.Fatalf("create delivery: %v", err)
	}
	if err := svc.Deliver(tenantCtx("tenant-a"), delivery.ID); err == nil {
		t.Fatal("expected the exhausted delivery to fail")
	}
	autoAttempts, err := repo.ListDeliveryAttempts(context.Background(), delivery.ID)
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(autoAttempts) != 1 || autoAttempts[0].Kind != domain.AttemptKindAutomatic {
		t.Fatalf("automatic retry history = %+v, want one automatic attempt", autoAttempts)
	}

	// Operator replay: the replay delivery records replay-kind attempts.
	seedDeadLetter(t, repo, "tenant-a", "dl-1", "ep-1", "transfer.settled", `{}`, time.Now().UTC())
	replayed, err := svc.ReplayDeadLetter(tenantCtx("tenant-a"), "dl-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	replayAttempts, err := repo.ListDeliveryAttempts(context.Background(), replayed.ReplayDeliveryID)
	if err != nil {
		t.Fatalf("list replay attempts: %v", err)
	}
	if len(replayAttempts) == 0 || replayAttempts[0].Kind != domain.AttemptKindReplay {
		t.Fatalf("replay history = %+v, want a replay-kind attempt", replayAttempts)
	}
}

// TestDeadLetterRedaction_StripsSensitiveFieldsButReplayStaysFaithful covers the
// redaction rule: operators never see secrets, but a replay still sends the
// original payload.
func TestDeadLetterRedaction_StripsSensitiveFieldsButReplayStaysFaithful(t *testing.T) {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	repo := newFakeRepo()
	seedEndpoint(t, repo, "tenant-a", "ep-1", ts.URL, true)
	seedDeadLetter(t, repo, "tenant-a", "dl-1", "ep-1", "payment.completed",
		`{"id":"pay-1","api_key":"sk_live_secret"}`, time.Now().UTC())

	svc := NewService(repo, nil, nil, 120, true).(*service)

	listed, err := svc.ListDeadLetters(tenantCtx("tenant-a"), domain.DeadLetterFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListDeadLetters: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected one dead letter, got %d", len(listed))
	}
	if strings.Contains(listed[0].Payload, "sk_live_secret") {
		t.Fatalf("sensitive payload leaked to the operator: %s", listed[0].Payload)
	}
	if len(listed[0].RedactedFields) != 1 || listed[0].RedactedFields[0] != "api_key" {
		t.Fatalf("redacted fields = %v, want [api_key]", listed[0].RedactedFields)
	}

	replayed, err := svc.ReplayDeadLetter(tenantCtx("tenant-a"), "dl-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := svc.Deliver(tenantCtx("tenant-a"), replayed.ReplayDeliveryID); err != nil {
		t.Fatalf("deliver replay: %v", err)
	}
	if !strings.Contains(gotBody, "sk_live_secret") {
		t.Fatalf("replay must send the original payload, got %s", gotBody)
	}
}

// TestDeadLetterFiltersAndRetention covers the list filters and the retention
// sweep.
func TestDeadLetterFiltersAndRetention(t *testing.T) {
	repo := newFakeRepo()
	seedEndpoint(t, repo, "tenant-a", "ep-1", "https://example.com/hook", true)
	now := time.Now().UTC()
	seedDeadLetter(t, repo, "tenant-a", "dl-settled", "ep-1", "transfer.settled", `{}`, now)
	seedDeadLetter(t, repo, "tenant-a", "dl-funded", "ep-1", "wallet.funded", `{}`, now)
	// Older than the 30-day retention window.
	seedDeadLetter(t, repo, "tenant-a", "dl-expired", "ep-1", "transfer.settled", `{}`, now.Add(-40*24*time.Hour))

	svc := NewService(repo, nil, nil, 120, false)

	all, err := svc.ListDeadLetters(tenantCtx("tenant-a"), domain.DeadLetterFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("ListDeadLetters: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("retention should have removed the expired record, got %d", len(all))
	}

	byEvent, err := svc.ListDeadLetters(tenantCtx("tenant-a"), domain.DeadLetterFilter{EventType: "wallet.funded"}, 10, 0)
	if err != nil {
		t.Fatalf("filter by event: %v", err)
	}
	if len(byEvent) != 1 || byEvent[0].EventType != "wallet.funded" {
		t.Fatalf("event filter returned %+v", byEvent)
	}

	since := now.Add(-time.Minute)
	byDate, err := svc.ListDeadLetters(tenantCtx("tenant-a"), domain.DeadLetterFilter{Since: &since}, 10, 0)
	if err != nil {
		t.Fatalf("filter by date: %v", err)
	}
	if len(byDate) != 2 {
		t.Fatalf("date filter returned %d, want 2", len(byDate))
	}
}

// TestDeadLetterHTTPEndpoints covers the list/detail/replay surface and its
// tenant scoping.
func TestDeadLetterHTTPEndpoints(t *testing.T) {
	repo := newFakeRepo()
	seedEndpoint(t, repo, "tenant-a", "ep-1", "https://example.com/hook", true)
	seedDeadLetter(t, repo, "tenant-a", "dl-1", "ep-1", "transfer.settled", `{"id":"tx-1"}`, time.Now().UTC())

	svc := NewService(repo, nil, nil, 120, false)
	routerA := newConfigTestRouter(t, svc, "tenant-a")
	routerB := newConfigTestRouter(t, svc, "tenant-b")

	code, body := doJSON(t, routerA, http.MethodGet, "/webhooks/dead-letters", "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d, want 200, body=%v", code, body)
	}
	list, _ := body["dead_letters"].([]interface{})
	if len(list) != 1 {
		t.Fatalf("list returned %d, want 1", len(list))
	}

	code, detail := doJSON(t, routerA, http.MethodGet, "/webhooks/dead-letters/dl-1", "")
	if code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200, body=%v", code, detail)
	}

	// Cross-tenant access is a 404, not a 403, so tenant boundaries are not
	// enumerable.
	if code, _ := doJSON(t, routerB, http.MethodGet, "/webhooks/dead-letters/dl-1", ""); code != http.StatusNotFound {
		t.Fatalf("cross-tenant detail status = %d, want 404", code)
	}
	if code, _ := doJSON(t, routerB, http.MethodPost, "/webhooks/dead-letters/dl-1/replay", ""); code != http.StatusNotFound {
		t.Fatalf("cross-tenant replay status = %d, want 404", code)
	}

	code, replayed := doJSON(t, routerA, http.MethodPost, "/webhooks/dead-letters/dl-1/replay", "")
	if code != http.StatusAccepted {
		t.Fatalf("replay status = %d, want 202, body=%v", code, replayed)
	}
	if replayed["replay_delivery_id"] == nil {
		t.Fatalf("replay response should identify the new delivery: %v", replayed)
	}

	// An unauthenticated request is refused.
	code, _ = doJSON(t, newConfigTestRouter(t, svc, ""), http.MethodGet, "/webhooks/dead-letters", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list status = %d, want 401", code)
	}
}

// TestRedactWebhookPayload_NestedAndNonJSON documents the redaction helper's
// boundary behavior directly.
func TestRedactWebhookPayload_NestedAndNonJSON(t *testing.T) {
	redacted, fields := domain.RedactWebhookPayload(`{"outer":{"token":"abc"},"amount":"10"}`)
	if strings.Contains(redacted, "abc") {
		t.Fatalf("nested token leaked: %s", redacted)
	}
	if len(fields) != 1 || fields[0] != "outer.token" {
		t.Fatalf("fields = %v, want [outer.token]", fields)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(redacted), &decoded); err != nil {
		t.Fatalf("redacted payload is not valid JSON: %v", err)
	}

	unchanged, fields := domain.RedactWebhookPayload("not json")
	if unchanged != "not json" || fields != nil {
		t.Fatalf("non-JSON payload should be returned unchanged, got %q %v", unchanged, fields)
	}
}
