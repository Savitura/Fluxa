package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

const (
	FiatStatusPending   = "pending"
	FiatStatusCompleted = "completed"
	FiatStatusFailed    = "failed"
)

type FiatDeposit struct {
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
