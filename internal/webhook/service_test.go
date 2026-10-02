package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

type fakeRepo struct {
	mu            sync.Mutex
	endpoints     map[string]*domain.WebhookEndpoint
	deliveries    map[string]*domain.WebhookDelivery
	deadLetters   map[string]*domain.WebhookDeadLetter
	attempts      map[string][]*domain.WebhookDeliveryAttempt
	subscriptions map[string]*domain.WebhookSubscription
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		endpoints:     make(map[string]*domain.WebhookEndpoint),
		deliveries:    make(map[string]*domain.WebhookDelivery),
		deadLetters:   make(map[string]*domain.WebhookDeadLetter),
		attempts:      make(map[string][]*domain.WebhookDeliveryAttempt),
		subscriptions: make(map[string]*domain.WebhookSubscription),
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

// cloneDeadLetter mirrors the production repository, which returns a fresh
// row per read. Returning the stored pointer would let a read-path redaction
// mutate the retained payload and corrupt a later replay.
func cloneDeadLetter(dl *domain.WebhookDeadLetter) *domain.WebhookDeadLetter {
	copy := *dl
	copy.RedactedFields = append([]string(nil), dl.RedactedFields...)
	return &copy
}

func (f *fakeRepo) GetDeadLetter(_ context.Context, id string) (*domain.WebhookDeadLetter, error) {
	dl, ok := f.deadLetters[id]
	if !ok {
		return nil, domain.ErrDeadLetterNotFound
	}
	return cloneDeadLetter(dl), nil
}

func (f *fakeRepo) GetDeadLetterForTenant(_ context.Context, id, tenantID string) (*domain.WebhookDeadLetter, error) {
	dl, ok := f.deadLetters[id]
	if !ok || dl.TenantID == nil || *dl.TenantID != tenantID {
		return nil, domain.ErrDeadLetterNotFound
	}
	return cloneDeadLetter(dl), nil
}

func (f *fakeRepo) ListDeadLetters(_ context.Context, filter domain.DeadLetterFilter, limit, offset int) ([]*domain.WebhookDeadLetter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res := make([]*domain.WebhookDeadLetter, 0)
	for _, dl := range f.deadLetters {
		if filter.TenantID != "" && (dl.TenantID == nil || *dl.TenantID != filter.TenantID) {
			continue
		}
		if filter.EndpointID != "" && dl.EndpointID != filter.EndpointID {
			continue
		}
		if filter.EventType != "" && dl.EventType != filter.EventType {
			continue
		}
		if filter.Status != "" && dl.Status != filter.Status {
			continue
		}
		if filter.Since != nil && dl.CreatedAt.Before(*filter.Since) {
			continue
		}
		if filter.Until != nil && dl.CreatedAt.After(*filter.Until) {
			continue
		}
		res = append(res, cloneDeadLetter(dl))
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.After(res[j].CreatedAt) })
	if offset > len(res) {
		return res[:0], nil
	}
	res = res[offset:]
	if limit > 0 && len(res) > limit {
		res = res[:limit]
	}
	return res, nil
}

func (f *fakeRepo) ClaimReplay(_ context.Context, deadLetterID, tenantID string, replay *domain.WebhookDelivery, now time.Time) (*domain.WebhookDeadLetter, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dl, ok := f.deadLetters[deadLetterID]
	if !ok || dl.TenantID == nil || *dl.TenantID != tenantID {
		return nil, false, domain.ErrDeadLetterNotFound
	}
	if dl.Status == domain.DeadLetterReplayed {
		return cloneDeadLetter(dl), false, nil
	}
	if dl.Status == domain.DeadLetterDiscarded {
		return nil, false, domain.ErrDeadLetterNotFound
	}
	f.deliveries[replay.ID] = replay
	dl.Status = domain.DeadLetterReplayed
	dl.ReplayCount++
	dl.LastReplayedAt = &now
	dl.ReplayToken = replay.ID
	dl.ReplayDeliveryID = replay.ID
	return cloneDeadLetter(dl), true, nil
}

func (f *fakeRepo) RecordDeliveryAttempt(_ context.Context, attempt *domain.WebhookDeliveryAttempt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts[attempt.DeliveryID] = append(f.attempts[attempt.DeliveryID], attempt)
	return nil
}

func (f *fakeRepo) ListDeliveryAttempts(_ context.Context, deliveryID string) ([]*domain.WebhookDeliveryAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*domain.WebhookDeliveryAttempt(nil), f.attempts[deliveryID]...), nil
}

func (f *fakeRepo) PruneDeadLetters(_ context.Context, before time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var removed int64
	for id, dl := range f.deadLetters {
		if dl.CreatedAt.Before(before) {
			delete(f.deadLetters, id)
			removed++
		}
	}
	return removed, nil
}

func (f *fakeRepo) CreateSubscription(_ context.Context, sub *domain.WebhookSubscription) error {
	f.subscriptions[sub.ID] = sub
	return nil
}

func (f *fakeRepo) DeleteSubscription(_ context.Context, id string) error {
	delete(f.subscriptions, id)
	return nil
}

func (f *fakeRepo) ListSubscriptions(_ context.Context, tenantID *string) ([]*domain.WebhookSubscription, error) {
	var res []*domain.WebhookSubscription
	for _, sub := range f.subscriptions {
		res = append(res, sub)
	}
	return res, nil
}

func (f *fakeRepo) GetSubscriptionsForEvent(_ context.Context, tenantID *string, eventType string) ([]*domain.WebhookSubscription, error) {
	var res []*domain.WebhookSubscription
	for _, sub := range f.subscriptions {
		if sub.EventType == eventType || sub.EventType == "*" {
			res = append(res, sub)
		}
	}
	return res, nil
}

func TestWebhookService_EncryptsEndpointSecretAndRedactsList(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, nil, nil, 120, false)
	endpoint, secret, err := svc.RegisterEndpoint(context.Background(), "https://example.com/webhook", nil)
	if err != nil {
		t.Fatalf("RegisterEndpoint: %v", err)
	}
	if endpoint.Secret != secret || secret == "" {
		t.Fatal("registration should disclose the generated secret once")
	}
	stored := repo.endpoints[endpoint.ID]
	if stored.Secret == secret || !strings.HasPrefix(stored.Secret, "v1::") {
		t.Fatal("endpoint secret must be encrypted at rest")
	}
	listed, err := svc.ListEndpoints(context.Background())
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(listed) != 1 || listed[0].Secret != "" {
		t.Fatalf("endpoint listing must redact secrets, got %+v", listed)
	}
}

func TestWebhookService_MaxAttemptsAndDeadLetter(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, nil, nil, 120, false)

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

	dls, err := repo.ListDeadLetters(context.Background(), domain.DeadLetterFilter{}, 10, 0)
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
	svc, ok := NewService(repo, nil, nil, 120, false).(*service)
	if !ok {
		t.Fatal("NewService did not return *service")
	}
	svc.allowPrivateNetworks = true // the destination is a loopback httptest server

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
