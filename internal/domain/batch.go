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
	// compliance review.
	BatchStatusComplianceHold BatchStatus = "compliance_hold"
)

// BatchTransfer is the parent record of a multi-recipient transfer: one row
// per POST /v1/transfers/batch call, linked to its individual transactions
// through transactions.batch_id. The aggregate columns are written by
// SyncAggregates under a row lock so concurrent workers settling sibling
// transfers cannot lose a count update.
type BatchTransfer struct {
	ID             string
	TenantID       *string
	Status         BatchStatus
	TotalCount     int
	SucceededCount int
	FailedCount    int
	SettledAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// IsTerminalBatchStatus reports whether a batch status will not change again
// on its own. Compliance holds are not terminal: a reviewer can still approve
// or reject the parked children, which puts the batch back in motion.
func IsTerminalBatchStatus(s BatchStatus) bool {
	switch s {
	case BatchStatusCompleted, BatchStatusPartial, BatchStatusFailed:
		return true
	default:
		return false
	}
}

// SummarizeBatch derives the batch-level status and aggregate counts from its
// linked transactions' current settlement status, so a batch always reflects
// live worker progress. The status precedence matches the historical
// aggregate: an all-resolved batch with any parked hold reads as
// compliance_hold, nothing resolved reads as pending, a partially resolved
// batch reads as processing, and only a fully resolved batch may read as
// completed, failed or partial.
func SummarizeBatch(txs []*Transaction) (BatchStatus, int, int, int) {
	var succeeded, failed, held int
	for _, tx := range txs {
		switch tx.Status {
		case StatusConfirmed, StatusSettled:
			succeeded++
		case StatusFailed, StatusReconciliationFailed, StatusCancelled, StatusReversed:
			failed++
		case StatusComplianceHold:
			held++
		}
	}

	total := len(txs)
	resolved := succeeded + failed

	var status BatchStatus
	switch {
	// Held children need an explicit arm ahead of the progress checks. They
	// are not "processing" — nothing will move them without a human decision —
	// and reporting a batch as completed or failed while one is still parked
	// would hide the hold entirely.
	case held > 0 && resolved+held == total:
		status = BatchStatusComplianceHold
	case resolved == 0:
		status = BatchStatusPending
	case resolved < total:
		status = BatchStatusProcessing
	case failed == total:
		status = BatchStatusFailed
	case succeeded == total:
		status = BatchStatusCompleted
	default:
		status = BatchStatusPartial
	}
	return status, succeeded, failed, held
}
