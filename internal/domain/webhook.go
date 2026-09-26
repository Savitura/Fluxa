package domain

import "time"

type EventType string

const (
	EventTransferInitiated      EventType = "transfer.initiated"
	EventTransferSettled        EventType = "transfer.settled"
	EventTransferFailed         EventType = "transfer.failed"
	EventWalletFunded           EventType = "wallet.funded"
	EventConversionCompleted    EventType = "conversion.completed"
	EventTreasurySweepCompleted EventType = "treasury.sweep_completed"
	EventReconciliationDrift    EventType = "reconciliation.drift"
)

type DeliveryStatus string

const (
	DeliveryPending DeliveryStatus = "pending"
	DeliverySuccess DeliveryStatus = "success"
	DeliveryFailed  DeliveryStatus = "failed"
	DeliveryPaused  DeliveryStatus = "paused"
)

type WebhookEndpoint struct {
	ID        string
	TenantID  *string
	URL       string
	Secret    string
	Events    []string
	Active    bool
	CreatedAt time.Time
}

type WebhookDelivery struct {
	ID           string
	EndpointID   string
	EventType    EventType
	Payload      []byte
	Status       DeliveryStatus
	ResponseCode *int
	AttemptCount int
	LastAttempt  *time.Time
	CreatedAt    time.Time
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
