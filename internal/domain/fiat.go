package domain

import (
	"github.com/shopspring/decimal"
	"time"
)

const (
	FiatStatusPending = "pending"
	// FiatStatusProcessing is a short-lived state a deposit occupies between
	// being atomically claimed by a webhook handler and the corresponding
	// on-chain credit completing, so a concurrent/duplicate webhook delivery
	// for the same event can never also claim it and double-credit the user.
	FiatStatusProcessing = "processing"
	FiatStatusCompleted  = "completed"
	FiatStatusFailed     = "failed"
)

type FiatDeposit struct {
	ID                string
	WalletID          string
	Provider          string
	ProviderReference string
	FiatAmount        decimal.Decimal
	FiatCurrency      string
	USDCAmount        decimal.Decimal
	Instructions      map[string]string
	Status            string
	CreatedAt         time.Time
}

type FiatWithdrawal struct {
	ID                string
	WalletID          string
	Provider          string
	ProviderReference string
	FiatAmount        decimal.Decimal
	FiatCurrency      string
	USDCAmount        decimal.Decimal
	Status            string
	CreatedAt         time.Time
}
