package domain

import (
	"time"
)

type WebhookEndpoint struct {
	ID               string        `json:"id" db:"id"`
	TenantID         *string       `json:"tenant_id,omitempty" db:"tenant_id"`
	URL              string        `json:"url" db:"url"`
	Secret           string        `json:"secret,omitempty" db:"secret"`
	Events           []string      `json:"events" db:"events"`
	Active           bool          `json:"active" db:"active"`
	SuccessCount     int           `json:"success_count" db:"success_count"`
	FailureCount     int           `json:"failure_count" db:"failure_count"`
	LastDeliveredAt  *time.Time    `json:"last_delivered_at,omitempty" db:"last_delivered_at"`
	NotifiedFailing  bool          `json:"notified_failing" db:"notified_failing"`
	CreatedAt        time.Time     `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at" db:"updated_at"`
}

type WebhookDelivery struct {
	ID            string     `json:"id" db:"id"`
	EndpointID    string     `json:"endpoint_id" db:"endpoint_id"`
	TenantID      *string    `json:"tenant_id,omitempty" db:"tenant_id"`
	EventType     string     `json:"event_type" db:"event_type"`
	Payload       string     `json:"payload" db:"payload"`
	Status        string     `json:"status" db:"status"` // pending, success, failed, dead_lettered
	ResponseCode  *int       `json:"response_code,omitempty" db:"response_code"`
	ResponseBody  *string    `json:"response_body,omitempty" db:"response_body"`
	ErrorMessage  *string    `json:"error_message,omitempty" db:"error_message"`
	AttemptCount  int        `json:"attempt_count" db:"attempt_count"`
	MaxAttempts   int        `json:"max_attempts" db:"max_attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty" db:"next_attempt_at"`
	LastAttempt   *time.Time `json:"last_attempt,omitempty" db:"last_attempt"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at" db:"updated_at"`
}

type WebhookDeadLetter struct {
	ID           string    `json:"id" db:"id"`
	EndpointID   string    `json:"endpoint_id" db:"endpoint_id"`
	TenantID     *string   `json:"tenant_id,omitempty" db:"tenant_id"`
	DeliveryID   string    `json:"delivery_id" db:"delivery_id"`
	Payload      string    `json:"payload" db:"payload"`
	ErrorMessage string    `json:"error_message" db:"error_message"`
	AttemptCount int       `json:"attempt_count" db:"attempt_count"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

type WebhookHealth struct {
	EndpointID      string     `json:"endpoint_id" db:"endpoint_id"`
	URL             string     `json:"url" db:"url"`
	SuccessCount    int        `json:"success_count" db:"success_count"`
	FailureCount    int        `json:"failure_count" db:"failure_count"`
	LastDeliveredAt *time.Time `json:"last_delivered_at,omitempty" db:"last_delivered_at"`
	Failing         bool       `json:"failing" db:"failing"`
}
