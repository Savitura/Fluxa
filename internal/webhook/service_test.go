package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fluxa/fluxa/internal/domain"
)

// mockRepo implements Repository for testing.
type mockRepo struct {
	endpoints  map[string]*domain.WebhookEndpoint
	deliveries map[string]*domain.WebhookDelivery
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		endpoints:  make(map[string]*domain.WebhookEndpoint),
		deliveries: make(map[string]*domain.WebhookDelivery),
	}
}

func (m *mockRepo) Create(_ context.Context, ep *domain.WebhookEndpoint) error {
	m.endpoints[ep.ID] = ep
	return nil
}

func (m *mockRepo) GetByID(_ context.Context, id string) (*domain.WebhookEndpoint, error) {
	ep, ok := m.endpoints[id]
	if !ok {
		return nil, domain.ErrWebhookNotFound
	}
	return ep, nil
}

func (m *mockRepo) List(_ context.Context, _ *string) ([]*domain.WebhookEndpoint, error) {
	var out []*domain.WebhookEndpoint
	for _, ep := range m.endpoints {
		out = append(out, ep)
	}
	return out, nil
}

func (m *mockRepo) Delete(_ context.Context, id string, _ *string) error {
	if _, ok := m.endpoints[id]; !ok {
		return domain.ErrWebhookNotFound
	}
	delete(m.endpoints, id)
	return nil
}

func (m *mockRepo) ListActiveByEvent(_ context.Context, eventType string) ([]*domain.WebhookEndpoint, error) {
	var out []*domain.WebhookEndpoint
	for _, ep := range m.endpoints {
		if !ep.Active {
			continue
		}
		if len(ep.Events) == 0 {
			out = append(out, ep)
			continue
		}
		for _, e := range ep.Events {
			if e == eventType {
				out = append(out, ep)
				break
			}
		}
	}
	return out, nil
}

func (m *mockRepo) CreateDelivery(_ context.Context, d *domain.WebhookDelivery) error {
	m.deliveries[d.ID] = d
	return nil
}

func (m *mockRepo) UpdateDelivery(_ context.Context, d *domain.WebhookDelivery) error {
	m.deliveries[d.ID] = d
	return nil
}

func (m *mockRepo) GetDeliveryByID(_ context.Context, id string, _ *string) (*domain.WebhookDelivery, error) {
	d, ok := m.deliveries[id]
	if !ok {
		return nil, domain.ErrWebhookDeliveryNotFound
	}
	return d, nil
}

func (m *mockRepo) ListDeliveries(_ context.Context, endpointID string, _, _ int, _ *string) ([]*domain.WebhookDelivery, error) {
	var out []*domain.WebhookDelivery
	for _, d := range m.deliveries {
		if d.EndpointID == endpointID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (m *mockRepo) CountByTenant(_ context.Context, tenantID string) (int, error) {
	count := 0
	for _, ep := range m.endpoints {
		if ep.TenantID != nil && *ep.TenantID == tenantID {
			count++
		}
	}
	return count, nil
}

func TestRegister(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil)

	ep, err := svc.Register(context.Background(), "https://example.com/hook", []string{"transfer.settled"})
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	if ep.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if ep.Secret == "" {
		t.Fatal("expected non-empty Secret")
	}
	if ep.URL != "https://example.com/hook" {
		t.Fatalf("URL = %q, want https://example.com/hook", ep.URL)
	}
	if !ep.Active {
		t.Fatal("expected endpoint to be active")
	}
}

func TestDelete_NotFound(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil)
	err := svc.Delete(context.Background(), "nonexistent")
	if err != domain.ErrWebhookNotFound {
		t.Fatalf("Delete() = %v, want ErrWebhookNotFound", err)
	}
}

func TestDispatchAndDeliver(t *testing.T) {
	// Start a test HTTP server that records requests.
	var received []byte
	var receivedSig, receivedTimestamp string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Fluxa-Signature")
		receivedTimestamp = r.Header.Get("X-Fluxa-Timestamp")
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	repo := newMockRepo()
	svc := NewService(repo, nil).(*service)
	svc.allowPrivateNetworks = true // ts.URL is a loopback httptest server

	// Register endpoint subscribed to all events.
	ep, _ := svc.Register(context.Background(), ts.URL, []string{})

	// Dispatch an event.
	type payload struct{ TxID string }
	err := svc.Dispatch(context.Background(), domain.EventTransferSettled, payload{TxID: "tx-1"})
	if err != nil {
		t.Fatalf("Dispatch() error: %v", err)
	}

	// Find the created delivery.
	var deliveryID string
	for id, d := range repo.deliveries {
		if d.EndpointID == ep.ID {
			deliveryID = id
			break
		}
	}
	if deliveryID == "" {
		t.Fatal("no delivery record created")
	}

	// Deliver it.
	if err := svc.Deliver(context.Background(), deliveryID); err != nil {
		t.Fatalf("Deliver() error: %v", err)
	}

	d := repo.deliveries[deliveryID]
	if d.Status != domain.DeliverySuccess {
		t.Fatalf("delivery status = %s, want success", d.Status)
	}
	if d.ResponseCode == nil || *d.ResponseCode != 200 {
		t.Fatal("expected response_code 200")
	}
	if receivedSig == "" {
		t.Fatal("expected X-Fluxa-Signature header")
	}
	if receivedTimestamp == "" {
		t.Fatal("expected X-Fluxa-Timestamp header")
	}
	if got, want := receivedSig, sign(ep.Secret, receivedTimestamp, d.Payload); got != want {
		t.Fatalf("signature = %s, want %s (signed over timestamp+\".\"+payload)", got, want)
	}
	if valid := Verify(ep.Secret, receivedTimestamp, string(d.Payload), receivedSig); !valid.Valid {
		t.Fatalf("Verify() rejected a delivery this service just signed: %s", valid.Reason)
	}
}

func TestSign_Deterministic(t *testing.T) {
	secret := "test-secret"
	timestamp := "1700000000"
	payload := []byte(`{"event":"transfer.settled"}`)
	sig1 := sign(secret, timestamp, payload)
	sig2 := sign(secret, timestamp, payload)
	if sig1 != sig2 {
		t.Fatal("sign() is not deterministic")
	}
	if len(sig1) < 7 || sig1[:7] != "sha256=" {
		t.Fatalf("signature format wrong: %s", sig1)
	}
	if otherTimestamp := sign(secret, "1700000001", payload); otherTimestamp == sig1 {
		t.Fatal("sign() must produce a different signature for a different timestamp — the timestamp is part of the signed payload, not just a side-channel header")
	}
}

func TestDispatch_FiltersByEvent(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil)

	// Endpoint subscribed only to transfer.failed
	ep := &domain.WebhookEndpoint{
		ID:        "ep-1",
		URL:       "https://example.com",
		Secret:    "secret",
		Events:    []string{"transfer.failed"},
		Active:    true,
		CreatedAt: time.Now(),
	}
	repo.endpoints[ep.ID] = ep

	// Dispatch transfer.settled — should not create a delivery for this endpoint.
	_ = svc.Dispatch(context.Background(), domain.EventTransferSettled, map[string]string{"id": "tx-1"})

	for _, d := range repo.deliveries {
		if d.EndpointID == ep.ID {
			t.Fatal("delivery should not have been created for unsubscribed event")
		}
	}
}

// ---------------------------------------------------------------------------
// truncatePayload tests
// ---------------------------------------------------------------------------

func TestTruncatePayload_SmallPayloadUntouched(t *testing.T) {
	in := []byte(`{"event":"transfer.settled","id":"tx-1"}`)
	out := truncatePayload(in)
	if string(out) != string(in) {
		t.Fatalf("small payload was modified: got %q", out)
	}
}

func TestTruncatePayload_ExactLimitUntouched(t *testing.T) {
	in := make([]byte, maxPayloadSize)
	for i := range in {
		in[i] = 'A'
	}
	out := truncatePayload(in)
	if len(out) != maxPayloadSize {
		t.Fatalf("exact-limit payload was resized: got %d bytes, want %d", len(out), maxPayloadSize)
	}
}

func TestTruncatePayload_LargePayloadTruncated(t *testing.T) {
	in := make([]byte, maxPayloadSize+1000)
	for i := range in {
		in[i] = 'B'
	}
	out := truncatePayload(in)
	if len(out) != maxPayloadSize {
		t.Fatalf("truncated payload size = %d, want %d", len(out), maxPayloadSize)
	}
	if !bytes.HasSuffix(out, truncationMarker) {
		t.Fatalf("truncated payload does not end with marker: %q", out[len(out)-30:])
	}
}

func TestTruncatePayload_RespectsUTF8Boundaries(t *testing.T) {
	// Build a payload with multi-byte UTF-8 characters at the boundary.
	// "€" is 3 bytes (0xE2 0x82 0xAC). Place one straddling the limit.
	base := make([]byte, maxPayloadSize-1)
	for i := range base {
		base[i] = 'C'
	}
	// Append a 3-byte character that would straddle the boundary.
	in := append(base, []byte("€")...) // total = maxPayloadSize + 2
	out := truncatePayload(in)
	if len(out) != maxPayloadSize {
		t.Fatalf("UTF-8 truncated payload size = %d, want %d", len(out), maxPayloadSize)
	}
	if !bytes.HasSuffix(out, truncationMarker) {
		t.Fatalf("missing truncation marker")
	}
	// The body before the marker should be valid UTF-8.
	body := out[:len(out)-len(truncationMarker)]
	if !utf8.Valid(body) {
		t.Fatal("truncated body is not valid UTF-8")
	}
}

func TestTruncatePayload_EmojiAtBoundary(t *testing.T) {
	// "🎉" is 4 bytes. Build payload so it straddles the limit.
	base := make([]byte, maxPayloadSize-2)
	for i := range base {
		base[i] = 'D'
	}
	in := append(base, []byte("🎉")...) // total = maxPayloadSize + 2
	out := truncatePayload(in)
	if len(out) != maxPayloadSize {
		t.Fatalf("emoji truncated payload size = %d, want %d", len(out), maxPayloadSize)
	}
	body := out[:len(out)-len(truncationMarker)]
	if !utf8.Valid(body) {
		t.Fatal("truncated body is not valid UTF-8")
	}
}

func TestTruncatePayload_JsonWithBinaryData(t *testing.T) {
	// Simulate a JSON payload containing base64-encoded binary data
	// (like serialized ScVal bytes) that exceeds the limit.
	binaryData := make([]byte, maxPayloadSize)
	for i := range binaryData {
		binaryData[i] = byte(i % 256)
	}
	payload := map[string]interface{}{
		"event":    "transfer.settled",
		"raw_data": string(binaryData),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := truncatePayload(body)
	if len(out) > maxPayloadSize {
		t.Fatalf("payload too large after truncation: %d bytes", len(out))
	}
	if !bytes.HasSuffix(out, truncationMarker) {
		t.Fatal("missing truncation marker")
	}
}

func TestTruncatePayload_AllMultiByte(t *testing.T) {
	// Payload made entirely of 3-byte UTF-8 characters ("€").
	// Ensure truncation never splits a character. The result may be slightly
	// smaller than maxPayloadSize because we round down to a character boundary.
	char := []byte("€") // 3 bytes
	count := (maxPayloadSize / len(char)) + 10
	in := make([]byte, 0, count*len(char))
	for i := 0; i < count; i++ {
		in = append(in, char...)
	}
	out := truncatePayload(in)
	if len(out) > maxPayloadSize {
		t.Fatalf("all-multibyte truncated size = %d, exceeds max %d", len(out), maxPayloadSize)
	}
	if len(out) <= len(truncationMarker) {
		t.Fatalf("truncated payload too small: %d bytes", len(out))
	}
	body := out[:len(out)-len(truncationMarker)]
	if !utf8.Valid(body) {
		t.Fatal("truncated body contains invalid UTF-8")
	}
}
