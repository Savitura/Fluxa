package domain

import (
	"time"
)

// EventType is a string type for webhook event type constants.
type EventType string

const (
	EventTransferSettled        = "transfer.settled"
	EventTransferFailed         = "transfer.failed"
	EventWalletFunded           = "wallet.funded"
	EventTreasurySweepCompleted = "treasury.sweep_completed"
	EventReconciliationDrift    = "reconciliation.drift"

	EventTransferComplianceHold     = "transfer.compliance.hold"
	EventTransferComplianceApproved = "transfer.compliance.approved"
	EventTransferComplianceRejected = "transfer.compliance.rejected"
	EventSanctionsRefreshFailed     = "sanctions.refresh.failed"

	EventBatchCompleted       = "batch.completed"
	EventBatchPartiallyFailed = "batch.partially_failed"

	EventClaimableBalanceCreated = "claimable_balance.created"
	EventClaimableBalanceClaimed = "claimable_balance.claimed"
	EventClaimableBalanceExpired = "claimable_balance.expired"
	EventClaimableBalanceRevoked = "claimable_balance.revoked"

	EventAPIKeyRotationReminder = "api_key.rotation_reminder"
	EventAPIKeyExpired          = "api_key.expired"
)

// SupportedEventTypes, IsSupportedEventType, and the descriptive metadata for
// each event live in webhook_events.go, which owns the event contract as a
// single table rather than restating it here.

type WebhookEndpoint struct {
	ID              string     `json:"id"`
	TenantID        *string    `json:"tenant_id,omitempty"`
	URL             string     `json:"url"`
	Secret          string     `json:"secret,omitempty"`
	Events          []string   `json:"events"`
	Mode            Mode       `json:"mode"`
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
	Mode       Mode      `json:"mode"`
	WebhookURL string    `json:"webhook_url"`
	CreatedAt  time.Time `json:"created_at"`
}

type WebhookDelivery struct {
	ID            string     `json:"id"`
	EndpointID    string     `json:"endpoint_id"`
	TenantID      *string    `json:"tenant_id,omitempty"`
	Mode          Mode       `json:"mode"`
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
	Mode         Mode      `json:"mode"`
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
	SigningKeyID     string
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

type DeliveryStatus string

const (
	DeliveryPending DeliveryStatus = "pending"
	DeliverySuccess DeliveryStatus = "success"
	DeliveryFailed  DeliveryStatus = "failed"
	DeliveryPaused  DeliveryStatus = "paused"
)

type TenantWebhookDelivery struct {
	ID           string         `json:"id"`
	TenantID     string         `json:"tenant_id"`
	SigningKeyID string         `json:"signing_key_id,omitempty"`
	EventType    EventType      `json:"event_type"`
	Payload      []byte         `json:"payload"`
	Status       DeliveryStatus `json:"status"`
	ResponseCode *int           `json:"response_code,omitempty"`
	AttemptCount int            `json:"attempt_count"`
	LastAttempt  *time.Time     `json:"last_attempt,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type WebhookSigningSecret struct {
	KeyID       string     `json:"key_id"`
	CreatedAt   time.Time  `json:"created_at"`
	ActivatedAt time.Time  `json:"activated_at"`
	RetiredAt   *time.Time `json:"retired_at,omitempty"`
	Status      string     `json:"status"`
}

// WebhookConfigUpdate is a partial update: every field is a pointer so callers
// can distinguish "field omitted" from "field set to the zero value". Events is
// *[]string for the same reason — an explicit empty list clears subscriptions,
// while omitting it leaves the current list untouched.
type WebhookConfigUpdate struct {
	Enabled      *bool      `json:"enabled,omitempty"`
	URL          *string    `json:"url,omitempty"`
	Events       *[]string  `json:"events,omitempty"`
	Paused       *bool      `json:"paused,omitempty"`
	ResumeAt     *time.Time `json:"resume_at,omitempty"`
	RotateSecret bool       `json:"rotate_secret,omitempty"`
}

type WebhookConfigResult struct {
	Config *TenantWebhookConfig
	Secret string
}
