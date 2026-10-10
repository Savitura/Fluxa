package domain

import "time"

// WalletBalanceAlertStatus describes the current state of a balance alert.
type WalletBalanceAlertStatus string

const (
	WalletBalanceAlertActive   WalletBalanceAlertStatus = "active"
	WalletBalanceAlertInactive WalletBalanceAlertStatus = "inactive"
)

// WalletBalanceAlert represents a per-asset low-balance threshold configuration
// for a wallet. When the wallet's balance for the specified asset falls below
// the threshold, an alert is triggered.
type WalletBalanceAlert struct {
	ID          string                   `json:"id"`
	TenantID    string                   `json:"-"`
	Mode        Mode                     `json:"mode"`
	WalletID    string                   `json:"wallet_id"`
	AssetCode   string                   `json:"asset_code"`
	AssetIssuer string                   `json:"asset_issuer,omitempty"`
	Threshold   string                   `json:"threshold"` // Decimal string for precision
	Status      WalletBalanceAlertStatus `json:"status"`
	CreatedAt   time.Time                `json:"created_at"`
	UpdatedAt   time.Time                `json:"updated_at"`
}

// WalletBalanceAlertEvent represents a triggered alert event when a balance
// falls below the configured threshold.
type WalletBalanceAlertEvent struct {
	ID          string    `json:"id"`
	AlertID     string    `json:"alert_id"`
	WalletID    string    `json:"wallet_id"`
	AssetCode   string    `json:"asset_code"`
	AssetIssuer string    `json:"asset_issuer,omitempty"`
	Balance     string    `json:"balance"`
	Threshold   string    `json:"threshold"`
	TriggeredAt time.Time `json:"triggered_at"`
}
