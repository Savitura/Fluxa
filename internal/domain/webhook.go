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

	EventTransferComplianceHold     EventType = "transfer.compliance_hold"
	EventTransferComplianceApproved EventType = "transfer.compliance_approved"
	EventTransferComplianceRejected EventType = "transfer.compliance_rejected"
	EventSanctionsRefreshFailed     EventType = "sanctions.refresh_failed"
)

type DeliveryStatus string

const (
	DeliveryPending DeliveryStatus = "pending"
	DeliverySuccess DeliveryStatus = "success"
	DeliveryFailed  DeliveryStatus = "failed"
)

type WebhookEndpoint struct {
	ID        string
	TenantID  *string
	URL       string
	Secret    string   // HMAC signing secret
	Events    []string // subscribed event types; empty = all
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
