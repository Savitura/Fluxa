package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func (m *mockRepo) Delete(_ context.Context, id string) error {
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

func (m *mockRepo) GetDeliveryByID(_ context.Context, id string) (*domain.WebhookDelivery, error) {
	d, ok := m.deliveries[id]
	if !ok {
		return nil, domain.ErrWebhookDeliveryNotFound
	}
	return d, nil
}

func (m *mockRepo) ListDeliveries(_ context.Context, endpointID string, _, _ int) ([]*domain.WebhookDelivery, error) {
	var out []*domain.WebhookDelivery
	for _, d := range m.deliveries {
		if d.EndpointID == endpointID {
			out = append(out, d)
		}
	}
	return out, nil
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
	var receivedSig string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Fluxa-Signature")
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	repo := newMockRepo()
	svc := NewService(repo, nil)

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
}

func TestSign_Deterministic(t *testing.T) {
	secret := "test-secret"
	payload := []byte(`{"event":"transfer.settled"}`)
	sig1 := sign(secret, payload)
	sig2 := sign(secret, payload)
	if sig1 != sig2 {
		t.Fatal("sign() is not deterministic")
	}
	if len(sig1) < 7 || sig1[:7] != "sha256=" {
		t.Fatalf("signature format wrong: %s", sig1)
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
