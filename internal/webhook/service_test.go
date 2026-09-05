package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

type fakeRepo struct {
	endpoints  map[string]*domain.WebhookEndpoint
	deliveries map[string]*domain.WebhookDelivery
	deadLetters map[string]*domain.WebhookDeadLetter
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		endpoints:   make(map[string]*domain.WebhookEndpoint),
		deliveries:  make(map[string]*domain.WebhookDelivery),
		deadLetters: make(map[string]*domain.WebhookDeadLetter),
	}
}

func (f *fakeRepo) CreateEndpoint(_ context.Context, ep *domain.WebhookEndpoint) error {
	f.endpoints[ep.ID] = ep
	return nil
}

func (f *fakeRepo) GetEndpoint(_ context.Context, id string) (*domain.WebhookEndpoint, error) {
	ep, ok := f.endpoints[id]
	if !ok {
		return nil, http.ErrMissingBoundary
	}
	return ep, nil
}

func (f *fakeRepo) ListEndpoints(_ context.Context, tenantID *string) ([]*domain.WebhookEndpoint, error) {
	var res []*domain.WebhookEndpoint
	for _, ep := range f.endpoints {
		res = append(res, ep)
	}
	return res, nil
}

func (f *fakeRepo) UpdateEndpoint(_ context.Context, ep *domain.WebhookEndpoint) error {
	f.endpoints[ep.ID] = ep
	return nil
}

func (f *fakeRepo) DeleteEndpoint(_ context.Context, id string) error {
	delete(f.endpoints, id)
	return nil
}

func (f *fakeRepo) CreateDelivery(_ context.Context, d *domain.WebhookDelivery) error {
	f.deliveries[d.ID] = d
	return nil
}

func (f *fakeRepo) GetDelivery(_ context.Context, id string) (*domain.WebhookDelivery, error) {
	d, ok := f.deliveries[id]
	if !ok {
		return nil, http.ErrMissingBoundary
	}
	return d, nil
}

func (f *fakeRepo) UpdateDelivery(_ context.Context, d *domain.WebhookDelivery) error {
	f.deliveries[d.ID] = d
	return nil
}

func (f *fakeRepo) ListDeliveries(_ context.Context, endpointID string, limit, offset int) ([]*domain.WebhookDelivery, error) {
	var res []*domain.WebhookDelivery
	for _, d := range f.deliveries {
		if d.EndpointID == endpointID {
			res = append(res, d)
		}
	}
	return res, nil
}

func (f *fakeRepo) CreateDeadLetter(_ context.Context, dl *domain.WebhookDeadLetter) error {
	f.deadLetters[dl.ID] = dl
	return nil
}

func (f *fakeRepo) GetDeadLetter(_ context.Context, id string) (*domain.WebhookDeadLetter, error) {
	dl, ok := f.deadLetters[id]
	if !ok {
		return nil, http.ErrMissingBoundary
	}
	return dl, nil
}

func (f *fakeRepo) ListDeadLetters(_ context.Context, tenantID *string, limit, offset int) ([]*domain.WebhookDeadLetter, error) {
	var res []*domain.WebhookDeadLetter
	for _, dl := range f.deadLetters {
		res = append(res, dl)
	}
	return res, nil
}

func TestWebhookService_MaxAttemptsAndDeadLetter(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, nil, nil, 120)

	ep, _, err := svc.RegisterEndpoint(context.Background(), "https://example.com/webhook", nil)
	if err != nil {
		t.Fatalf("RegisterEndpoint error: %v", err)
	}

	deliv := &domain.WebhookDelivery{
		ID:           "del-1",
		EndpointID:   ep.ID,
		EventType:    "transfer.settled",
		Payload:      "{}",
		Status:       "pending",
		AttemptCount: 4,
		MaxAttempts:  5,
	}
	_ = repo.CreateDelivery(context.Background(), deliv)

	// Mock a failing server endpoint
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	ep.URL = ts.URL
	_ = repo.UpdateEndpoint(context.Background(), ep)

	// Deliver should fail on 5th attempt and push to dead-letter queue
	err = svc.Deliver(context.Background(), deliv.ID)
	if err == nil {
		t.Fatalf("expected deliver error due to max attempts reached, got nil")
	}

	dls, err := repo.ListDeadLetters(context.Background(), nil, 10, 0)
	if err != nil || len(dls) != 1 {
		t.Fatalf("expected 1 dead letter record, got %d (err: %v)", len(dls), err)
	}

	h, err := svc.GetEndpointHealth(context.Background(), ep.ID)
	if err != nil {
		t.Fatalf("GetEndpointHealth error: %v", err)
	}
	if !h.Failing {
		t.Fatalf("expected endpoint health failing = true")
	}
}

func TestWebhookService_RetryPreservesHTTPMethod(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, nil, nil, 120)

	ep, _, err := svc.RegisterEndpoint(context.Background(), "https://example.com/webhook", nil)
	if err != nil {
		t.Fatalf("RegisterEndpoint error: %v", err)
	}

	// Track the HTTP method used on each delivery attempt.
	var methods []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		// First attempt fails with 503, subsequent attempts succeed.
		if len(methods) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	ep.URL = ts.URL
	_ = repo.UpdateEndpoint(context.Background(), ep)

	deliv := &domain.WebhookDelivery{
		ID:           "del-method-1",
		EndpointID:   ep.ID,
		EventType:    "transfer.settled",
		Method:       http.MethodPost,
		Payload:      "{}",
		Status:       "pending",
		AttemptCount: 0,
		MaxAttempts:  5,
	}
	_ = repo.CreateDelivery(context.Background(), deliv)

	// First attempt: server returns 503, delivery fails and is re-queued.
	err = svc.Deliver(context.Background(), deliv.ID)
	if err == nil {
		t.Fatalf("expected first delivery to fail with 503, got nil")
	}

	// Second attempt (retry): should use the original POST method.
	err = svc.Deliver(context.Background(), deliv.ID)
	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}

	if len(methods) != 2 {
		t.Fatalf("expected 2 delivery attempts, got %d", len(methods))
	}
	for i, m := range methods {
		if m != http.MethodPost {
			t.Fatalf("attempt %d used method %q, want %q", i+1, m, http.MethodPost)
		}
	}
}
