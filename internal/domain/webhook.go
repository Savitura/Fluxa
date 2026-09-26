package domain

import (
	"time"
)

// EventType is a string type for webhook event type constants.
type EventType string

const (
	EventTypePaymentCompleted    = "payment.completed"
	EventTypePaymentFailed       = "payment.failed"
	EventTypeFxQuoteCreated      = "fx.quote.created"
	EventTypeSettlementCompleted = "settlement.completed"
	EventTypeBatchCompleted      = "batch.completed"

	EventTransferComplianceHold     = "transfer.compliance.hold"
	EventTransferComplianceApproved = "transfer.compliance.approved"
	EventTransferComplianceRejected = "transfer.compliance.rejected"
	EventSanctionsRefreshFailed     = "sanctions.refresh.failed"

	EventClaimableBalanceCreated = "claimable_balance.created"
	EventClaimableBalanceClaimed = "claimable_balance.claimed"
	EventClaimableBalanceExpired = "claimable_balance.expired"
	EventClaimableBalanceRevoked = "claimable_balance.revoked"
)

var SupportedEventTypes = []string{
	EventTypePaymentCompleted,
	EventTypePaymentFailed,
	EventTypeFxQuoteCreated,
	EventTypeSettlementCompleted,
	EventTypeBatchCompleted,
}

type WebhookEndpoint struct {
	ID              string     `json:"id"`
	TenantID        *string    `json:"tenant_id,omitempty"`
	URL             string     `json:"url"`
	Secret          string     `json:"secret,omitempty"`
	Events          []string   `json:"events"`
	Active          bool       `json:"active"`
	SuccessCount    int        `json:"success_count"`
	FailureCount    int        `json:"failure_count"`
	LastDeliveredAt *time.Time `json:"last_delivered_at,omitempty"`
	NotifiedFailing bool       `json:"notified_failing"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type WebhookSubscription struct {
	ID         string    `json:"id"`
	TenantID   *string   `json:"tenant_id,omitempty"`
	EventType  string    `json:"event_type"`
	WebhookURL string    `json:"webhook_url"`
	CreatedAt  time.Time `json:"created_at"`
}

type WebhookDelivery struct {
	ID            string     `json:"id"`
	EndpointID    string     `json:"endpoint_id"`
	TenantID      *string    `json:"tenant_id,omitempty"`
	EventType     string     `json:"event_type"`
	Method        string     `json:"method"`
	Payload       string     `json:"payload"`
	Status        string     `json:"status"`
	ResponseCode  int        `json:"response_code"`
	ResponseBody  string     `json:"response_body,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	AttemptCount  int        `json:"attempt_count"`
	MaxAttempts   int        `json:"max_attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	LastAttempt   *time.Time `json:"last_attempt,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type WebhookDeadLetter struct {
	ID           string    `json:"id"`
	EndpointID   string    `json:"endpoint_id"`
	TenantID     *string   `json:"tenant_id,omitempty"`
	DeliveryID   string    `json:"delivery_id"`
	Payload      string    `json:"payload"`
	ErrorMessage string    `json:"error_message"`
	AttemptCount int       `json:"attempt_count"`
	CreatedAt    time.Time `json:"created_at"`
}

type WebhookHealth struct {
	EndpointID      string     `json:"endpoint_id"`
	URL             string     `json:"url"`
	SuccessCount    int        `json:"success_count"`
	FailureCount    int        `json:"failure_count"`
	LastDeliveredAt *time.Time `json:"last_delivered_at,omitempty"`
	Failing         bool       `json:"failing"`
}

type TenantWebhookConfig struct {
	TenantID         string
	Enabled          bool
	URL              string
	Secret           string
	SigningAlgorithm string
	Events           []string
	Paused           bool
	ResumeAt         *time.Time
	LastDeliveredAt  *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	// SecretConfigured reports whether a signing secret exists, without
	// revealing it. It lets a client tell "secret set" from "no secret yet"
	// after the secret has been stripped from an API response.
	SecretConfigured bool
}

type TenantWebhookDelivery struct {
	ID           string
	TenantID     string
	EventType    EventType
	Payload      []byte
	Status       DeliveryStatus
	ResponseCode *int
	AttemptCount int
	LastAttempt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// WebhookConfigUpdate is a partial update: every field is a pointer so callers
// can distinguish "field omitted" from "field set to the zero value". Events is
// *[]string for the same reason — an explicit empty list clears subscriptions,
// while omitting it leaves the current list untouched.
type WebhookConfigUpdate struct {
	Enabled      *bool
	URL          *string
	Events       *[]string
	Paused       *bool
	ResumeAt     *time.Time
	RotateSecret bool
}

type WebhookConfigResult struct {
	Config *TenantWebhookConfig
	Secret string
}
