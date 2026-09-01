package domain

import (
	"time"
)

const (
	EventTypePaymentCompleted    = "payment.completed"
	EventTypePaymentFailed       = "payment.failed"
	EventTypeFxQuoteCreated      = "fx.quote.created"
	EventTypeSettlementCompleted = "settlement.completed"
	EventTypeBatchCompleted      = "batch.completed"
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
