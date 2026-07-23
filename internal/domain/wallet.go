package domain

import "time"

type Wallet struct {
	ID              string
	PublicKey       string
	EncryptedSecret string
	TenantID        *string
	CreatedAt       time.Time
	// SyncCursor is the Horizon paging token of the last payment operation
	// processed for this wallet, used to resume incremental sync.
	SyncCursor string
}
