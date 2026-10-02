package domain

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

	EventClaimableBalanceCreated = "claimable_balance.created"
	EventClaimableBalanceClaimed = "claimable_balance.claimed"
	EventClaimableBalanceExpired = "claimable_balance.expired"
	EventClaimableBalanceRevoked = "claimable_balance.revoked"

	EventAPIKeyRotationReminder = "api_key.rotation_reminder"
	EventAPIKeyExpired          = "api_key.expired"
)

var SupportedEventTypes = []string{
	EventTransferSettled,
	EventTransferFailed,
	EventWalletFunded,
	EventTreasurySweepCompleted,
	EventReconciliationDrift,
	EventTransferComplianceHold,
	EventTransferComplianceApproved,
	EventTransferComplianceRejected,
	EventSanctionsRefreshFailed,
	EventClaimableBalanceCreated,
	EventClaimableBalanceClaimed,
	EventClaimableBalanceExpired,
	EventClaimableBalanceRevoked,
	EventAPIKeyRotationReminder,
	EventAPIKeyExpired,
}

func IsSupportedEventType(eventType string) bool {
	for _, supported := range SupportedEventTypes {
		if eventType == supported {
			return true
		}
	}
	return false
}

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
	// ReplayOf is set when the delivery was created by an operator replay. It
	// holds the dead-letter ID being replayed, so attempt history can tell an
	// automatic retry from an operator replay.
	ReplayOf  string    `json:"replay_of,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DeadLetterStatus is the operator-facing state of a dead-lettered delivery.
type DeadLetterStatus string

const (
	// DeadLetterPending means the exhausted delivery is retained and has not
	// been replayed yet.
	DeadLetterPending DeadLetterStatus = "pending"
	// DeadLetterReplayed means an operator replayed it; ReplayDeliveryID points
	// at the delivery that resulted.
	DeadLetterReplayed DeadLetterStatus = "replayed"
	// DeadLetterDiscarded means an operator acknowledged it without replaying.
	DeadLetterDiscarded DeadLetterStatus = "discarded"
)

// AttemptKind distinguishes the worker's own retries from an operator replay.
type AttemptKind string

const (
	AttemptKindAutomatic AttemptKind = "automatic"
	AttemptKindReplay    AttemptKind = "replay"
)

// WebhookDeliveryAttempt is one recorded delivery attempt. The table is
// append-only and outlives the delivery so a dead-lettered event keeps its full
// history even after the delivery row is pruned.
type WebhookDeliveryAttempt struct {
	ID            string      `json:"id"`
	DeliveryID    string      `json:"delivery_id"`
	DeadLetterID  string      `json:"dead_letter_id,omitempty"`
	TenantID      *string     `json:"tenant_id,omitempty"`
	Mode          Mode        `json:"mode"`
	Kind          AttemptKind `json:"kind"`
	AttemptNumber int         `json:"attempt_number"`
	Status        string      `json:"status"`
	ResponseCode  int         `json:"response_code,omitempty"`
	ErrorMessage  string      `json:"error_message,omitempty"`
	OccurredAt    time.Time   `json:"occurred_at"`
}

type WebhookDeadLetter struct {
	ID             string           `json:"id"`
	EndpointID     string           `json:"endpoint_id"`
	TenantID       *string          `json:"tenant_id,omitempty"`
	Mode           Mode             `json:"mode"`
	DeliveryID     string           `json:"delivery_id"`
	EventType      string           `json:"event_type"`
	Payload        string           `json:"payload"`
	ErrorMessage   string           `json:"error_message"`
	AttemptCount   int              `json:"attempt_count"`
	Status         DeadLetterStatus `json:"status"`
	ReplayCount    int              `json:"replay_count"`
	LastReplayedAt *time.Time       `json:"last_replayed_at,omitempty"`
	ReplayToken    string           `json:"replay_token,omitempty"`
	// ReplayDeliveryID is the delivery a replay produced. It is empty until the
	// dead letter has been replayed.
	ReplayDeliveryID string `json:"replay_delivery_id,omitempty"`
	// RedactedFields lists the JSON paths stripped from Payload on read. The
	// stored payload itself is unmodified so a replay still carries the original
	// event body; redaction applies only to what an operator can see.
	RedactedFields []string   `json:"redacted_fields,omitempty"`
	RetainUntil    *time.Time `json:"retain_until,omitempty"`
	// Attempts is populated on the detail endpoint. The list endpoint leaves it
	// nil to keep responses bounded.
	Attempts  []*WebhookDeliveryAttempt `json:"attempts,omitempty"`
	CreatedAt time.Time                 `json:"created_at"`
}

// DeadLetterFilter narrows a tenant-scoped dead-letter listing. Zero values are
// ignored, so an empty filter lists everything the tenant owns.
type DeadLetterFilter struct {
	TenantID   string
	EndpointID string
	EventType  string
	Status     DeadLetterStatus
	Since      *time.Time
	Until      *time.Time
}

// sensitivePayloadKeys are matched case-insensitively as substrings of a JSON
// object key. The list errs toward over-redaction: showing an operator a
// redacted token is recoverable, leaking a live credential is not.
var sensitivePayloadKeys = []string{
	"secret", "token", "password", "passwd", "authorization", "auth",
	"api_key", "apikey", "private_key", "privatekey", "signature",
	"card", "cvv", "cvc", "ssn", "iban", "account_number",
}

// RedactWebhookPayload returns a copy of payload with sensitive object keys
// replaced by "[REDACTED]", plus the sorted list of redacted JSON paths. A
// payload that is not a JSON object (or not valid JSON) is returned unchanged:
// redaction must never turn an inspectable record into an error.
func RedactWebhookPayload(payload string) (string, []string) {
	var decoded interface{}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return payload, nil
	}
	fields := make([]string, 0)
	redacted := redactValue(decoded, "", &fields)
	if len(fields) == 0 {
		return payload, nil
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return payload, nil
	}
	sort.Strings(fields)
	return string(encoded), fields
}

func redactValue(value interface{}, path string, fields *[]string) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if isSensitiveKey(key) {
				out[key] = "[REDACTED]"
				*fields = append(*fields, childPath)
				continue
			}
			out[key] = redactValue(child, childPath, fields)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, child := range typed {
			childPath := fmt.Sprintf("%s.%d", path, index)
			if path == "" {
				childPath = fmt.Sprintf("%d", index)
			}
			out[index] = redactValue(child, childPath, fields)
		}
		return out
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, sensitive := range sensitivePayloadKeys {
		if strings.Contains(lower, sensitive) {
			return true
		}
	}
	return false
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
