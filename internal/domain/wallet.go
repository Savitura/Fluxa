package domain

import (
	"time"
)

type Wallet struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	PublicKey string    `json:"public_key"`
	CreatedAt time.Time `json:"created_at"`
}

type Balance struct {
	AssetCode   string `json:"asset_code"`
	AssetIssuer string `json:"asset_issuer,omitempty"`
	Balance     string `json:"balance"`
	Limit       string `json:"limit,omitempty"`
}

type WalletBalance struct {
	WalletID  string    `json:"wallet_id"`
	Balances  []Balance `json:"balances"`
	Stale     bool      `json:"stale"`
	UpdatedAt time.Time `json:"updated_at"`
}
