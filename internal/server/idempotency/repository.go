package idempotency

import (
	"context"
	"time"
)

const (
	StatusProcessing = "processing"
	StatusComplete   = "complete"
)

// Record is a persisted idempotency key and, once the original request has
// completed, the cached response to replay on any retry.
type Record struct {
	OrgID          string
	Key            string
	RequestHash    string
	Status         string
	ResponseStatus int
	ResponseBody   []byte
	ExpiresAt      time.Time
}

// Repository persists idempotency records scoped by (org_id, key).
type Repository interface {
	// TryAcquire attempts to claim the (orgID, key) pair for a new request.
	// If no record exists yet (or an expired one is cleared out of the way),
	// it inserts one with status "processing" and returns (nil, false, nil) —
	// the caller owns the request and should proceed to the handler.
	// If a live record already exists, it returns (record, true, nil).
	TryAcquire(ctx context.Context, orgID, key, requestHash string, expiresAt time.Time) (*Record, bool, error)
	// Complete marks a record as complete and stores the response to replay
	// on future retries of the same key.
	Complete(ctx context.Context, orgID, key string, responseStatus int, responseBody []byte) error
}
