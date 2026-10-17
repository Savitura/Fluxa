package fees

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Estimator is the preflight surface. It is deliberately separate from Service
// so existing fee-service doubles keep satisfying Service unchanged; the HTTP
// handler feature-detects it, matching the webhook config-service pattern.
type Estimator interface {
	Estimate(ctx context.Context, tenantID string, req EstimateRequest) (*NetworkFeeEstimate, error)
}

// ErrInvalidEstimateRequest is returned for a malformed preflight request. The
// HTTP handler surfaces it as a 400 so a caller can distinguish "your request
// is wrong" from "the fee schedule is unavailable".
var ErrInvalidEstimateRequest = errors.New("invalid fee estimate request")

// EstimateRequest is the preflight input for a transfer or a batch.
//
//   - Type is "transfer" or "batch".
//   - Amount is the total gross amount to move, across all destinations.
//   - Destinations is required for a batch and ignored for a transfer.
type EstimateRequest struct {
	Type         string
	Asset        string
	Amount       decimal.Decimal
	Destinations int
}

// NetworkFeeEstimate is the preflight result. Amounts are decimal strings
// because the platform carries 7-decimal precision and JSON floats would lose
// it. NetworkFee is denominated in the native asset (XLM) regardless of the
// asset being moved, since Stellar fees are always paid in XLM.
type NetworkFeeEstimate struct {
	Type                        string    `json:"type"`
	Asset                       string    `json:"asset"`
	GrossAmount                 string    `json:"gross_amount"`
	PlatformFee                 string    `json:"platform_fee"`
	PlatformFeeBps              int       `json:"platform_fee_bps"`
	NetAmount                   string    `json:"net_amount"`
	NetworkFee                  string    `json:"network_fee"`
	NetworkFeeStroops           int64     `json:"network_fee_stroops"`
	TotalFee                    string    `json:"total_fee"`
	BaseFeeStroops              int64     `json:"base_fee_stroops"`
	OperationCount              int       `json:"operation_count"`
	TransactionCount            int       `json:"transaction_count"`
	MaxOperationsPerTransaction int       `json:"max_operations_per_transaction"`
	ExpiresAt                   time.Time `json:"expires_at"`
}

// estimateTTL bounds how long a preflight result should be treated as current.
// Network fees move with ledger congestion, so a stale estimate must not be
// relied on indefinitely.
const estimateTTL = time.Minute

// operationCount returns how many Stellar operations the request needs and how
// many transactions those operations span. A single transfer is one payment
// operation; a batch is one payment per destination, packed into transactions
// of at most maxOps operations each.
func operationCount(req EstimateRequest, maxOps int) (operations, transactions int) {
	if req.Type == "batch" {
		operations = req.Destinations
	} else {
		operations = 1
	}
	if maxOps <= 0 {
		maxOps = DefaultMaxOperationsPerTransaction
	}
	transactions = (operations + maxOps - 1) / maxOps
	if transactions < 1 {
		transactions = 1
	}
	return operations, transactions
}

// validateEstimateRequest normalizes request and rejects anything that cannot
// be estimated. It returns the trimmed asset and the operation count.
func validateEstimateRequest(req EstimateRequest, maxOps int) (EstimateRequest, int, int, error) {
	switch req.Type {
	case "transfer", "batch":
	default:
		return req, 0, 0, ErrInvalidEstimateRequest
	}
	req.Asset = trimAsset(req.Asset)
	if req.Asset == "" {
		return req, 0, 0, ErrInvalidEstimateRequest
	}
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return req, 0, 0, ErrInvalidEstimateRequest
	}
	if req.Type == "transfer" {
		req.Destinations = 1
	}
	if req.Destinations < 1 || req.Destinations > MaxEstimateOperations {
		return req, 0, 0, ErrInvalidEstimateRequest
	}
	if maxOps <= 0 {
		maxOps = DefaultMaxOperationsPerTransaction
	}
	operations, transactions := operationCount(req, maxOps)
	return req, operations, transactions, nil
}

func trimAsset(asset string) string {
	return strings.TrimSpace(asset)
}
