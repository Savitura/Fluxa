package domain

import "time"

const (
	AccountTypeIndividual   = "individual"
	AccountTypeOrganization = "organization"
)

type Tenant struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	Email                 string     `json:"email"`
	AccountType           string     `json:"account_type"`
	MaxWallets            *int       `json:"max_wallets,omitempty"`
	MaxTransfersPerMonth  *int       `json:"max_transfers_per_month,omitempty"`
	MaxWebhooks           *int       `json:"max_webhooks,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
}

func (t *Tenant) GetWalletLimit() int {
	if t.MaxWallets != nil {
		return *t.MaxWallets
	}
	if t.AccountType == AccountTypeOrganization {
		return 50
	}
	return 5
}

func (t *Tenant) GetTransferLimit() int {
	if t.MaxTransfersPerMonth != nil {
		return *t.MaxTransfersPerMonth
	}
	if t.AccountType == AccountTypeOrganization {
		return -1 // unlimited
	}
	return 1000
}

func (t *Tenant) GetWebhookLimit() int {
	if t.MaxWebhooks != nil {
		return *t.MaxWebhooks
	}
	if t.AccountType == AccountTypeOrganization {
		return 10
	}
	return 2
}

