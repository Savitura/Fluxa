package domain

import "time"

type BatchStatus string

const (
	BatchStatusPending    BatchStatus = "pending"
	BatchStatusProcessing BatchStatus = "processing"
	BatchStatusPartial    BatchStatus = "partial"
	BatchStatusCompleted  BatchStatus = "completed"
	BatchStatusFailed     BatchStatus = "failed"
	// BatchStatusComplianceHold means every remaining child is parked awaiting
	// compliance review. It is derived for responses only and never written to
	// the batches table, so it is deliberately absent from the batch_status
	// enum in the database.
	BatchStatusComplianceHold BatchStatus = "compliance_hold"
)

type Batch struct {
	ID         string
	TenantID   *string
	Status     BatchStatus
	TotalCount int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
