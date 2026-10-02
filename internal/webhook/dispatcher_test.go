package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRedis struct {
	mr *miniredis.Miniredis
}

func newMockRedis(t *testing.T) *mockRedis {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	return &mockRedis{mr: mr}
}

func (m *mockRedis) Close() {
	m.mr.Close()
}

func (m *mockRedis) redisOpt() asynq.RedisConnOpt {
	return asynq.RedisClientOpt{Addr: m.mr.Addr()}
}

func (m *mockRedis) client() redis.UniversalClient {
	return redis.NewClient(&redis.Options{Addr: m.mr.Addr()})
}

func TestDispatcher_SendsNonEmptyBodyMatchingPayload(t *testing.T) {
	repo := newFakeRepo()

	var receivedBody string
	var receivedContentType string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		bs, _ := io.ReadAll(r.Body)
		receivedBody = string(bs)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ep := &domain.WebhookEndpoint{
		ID:        "ep-test-body",
		URL:       ts.URL,
		Secret:    "whsec_test",
		Active:    true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = repo.CreateEndpoint(context.Background(), ep)

	payloadObj := map[string]string{"event": "transfer.settled", "id": "tx-123"}
	bs, err := json.Marshal(payloadObj)
	if err != nil {
		t.Fatalf("unexpected error marshalling payload: %v", err)
	}
	payloadStr := string(bs)

	deliv := &domain.WebhookDelivery{
		ID:           "del-body-1",
		EndpointID:   ep.ID,
		EventType:    "transfer.settled",
		Method:       http.MethodPost,
		Payload:      payloadStr,
		Status:       "pending",
		AttemptCount: 0,
		MaxAttempts:  3,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	_ = repo.CreateDelivery(context.Background(), deliv)

	svc, ok := NewService(repo, nil, nil, 0, false).(*service)
	if !ok {
		t.Fatal("NewService did not return *service")
	}
	svc.allowPrivateNetworks = true // the destination is a loopback httptest server
	d := svc
	err = d.Deliver(context.Background(), deliv.ID)
	if err != nil {
		t.Fatalf("expected deliver success, got %v", err)
	}

	if receivedBody != payloadStr {
		t.Fatalf(
			"expected dispatched request body %q, got %q",
			payloadStr,
			receivedBody,
		)
	}

	if receivedContentType != "application/json" {
		t.Fatalf(
			"expected Content-Type application/json, got %q",
			receivedContentType,
		)
	}
}

func TestDispatcher_SSRFAndAsync(t *testing.T) {
	repo := newFakeRepo()
	rdb := newMockRedis(t)
	defer rdb.Close()

	rdbClient := rdb.client()
	defer rdbClient.Close()
	qClient := queue.NewClientWithOptions(rdb.redisOpt())
	defer qClient.Close()

	svc := NewService(repo, rdbClient, qClient, 120, false)
	Dispatcher := NewDispatcher(svc, qClient)

	// A loopback endpoint cannot be registered through the public API — the
	// SSRF guard rejects it — so seed it directly. Dispatch must skip it and
	// still report success, because one unsafe endpoint must not fail the event.
	loopbackEp := &domain.WebhookEndpoint{
		ID:        "ep-loopback",
		URL:       "http://127.0.0.1/webhook",
		Secret:    "whsec_loopback",
		Events:    []string{"transfer.settled"},
		Active:    true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.CreateEndpoint(context.Background(), loopbackEp))

	err := Dispatcher.Dispatch(context.Background(), "transfer.settled", map[string]string{"id": "tx_123"})
	assert.NoError(t, err)

	deliveries, err := repo.ListDeliveries(context.Background(), loopbackEp.ID, 10, 0)
	require.NoError(t, err)
	assert.Empty(t, deliveries, "an unsafe endpoint must not have a delivery queued")
}

func TestDispatcher_TimeoutAndMetadata(t *testing.T) {
	svc := NewService(newFakeRepo(), nil, nil, 120, false)
	// Verify metadata IP blocking logic directly
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	err := svc.(*service).validateWebhookURL(ctx, srv.URL)
	// Local server might resolve to 127.0.0.1 which is blocked unless allowPrivateNetworks is true
	assert.Error(t, err)
}

func TestDispatcher_ComprehensiveSSRFAndTimeoutScenarios(t *testing.T) {
	repo := newFakeRepo()
	rdb := newMockRedis(t)
	defer rdb.Close()

	rdbClient := rdb.client()
	defer rdbClient.Close()
	qClient := queue.NewClientWithOptions(rdb.redisOpt())
	defer qClient.Close()

	svc := NewService(repo, rdbClient, qClient, 120, false)
	_ = NewDispatcher(svc, qClient)

	ctx := context.Background()

	// 1. Loopback URL test
	err := svc.(*service).validateWebhookURL(ctx, "http://127.0.0.1:8080/webhook")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeWebhookURL)

	// 2. Link-local metadata URL test (169.254.169.254)
	err = svc.(*service).validateWebhookURL(ctx, "http://169.254.169.254/latest/meta-data/")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsafeWebhookURL)

	// 3. Redirect to internal host test
	internalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer internalSrv.Close()

	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internalSrv.URL, http.StatusFound)
	}))
	defer redirectSrv.Close()

	client := svc.(*service).client
	req, err := http.NewRequestWithContext(ctx, "POST", redirectSrv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	// Since redirects are disabled or checked, either Do returns an error or it doesn't follow to internalSrv
	// With CheckRedirect returning useLastResponse, resp.StatusCode should be 302, not 200.
	if resp != nil {
		assert.Equal(t, http.StatusFound, resp.StatusCode)
	}

	// 4. Endpoint that hangs past the timeout
	hangingSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer hangingSrv.Close()

	// Create client with a very short timeout for testing hang behavior
	shortClient := svc.(*service).newSafeHTTPClient()
	shortClient.Timeout = 20 * time.Millisecond

	hangReq, err := http.NewRequestWithContext(ctx, "POST", hangingSrv.URL, nil)
	require.NoError(t, err)
	_, err = shortClient.Do(hangReq)
	assert.Error(t, err)
}
