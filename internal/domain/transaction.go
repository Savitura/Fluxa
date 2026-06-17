package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type TransactionStatus string
type TransactionType string

const (
	StatusPending              TransactionStatus = "pending"
	StatusSubmitted            TransactionStatus = "submitted"
	StatusConfirmed            TransactionStatus = "confirmed"
	StatusFailed               TransactionStatus = "failed"
	StatusReconciliationFailed TransactionStatus = "reconciliation_failed"

	TypeTransfer   TransactionType = "transfer"
	TypeConversion TransactionType = "conversion"
	TypeFunding    TransactionType = "funding"
)

type Transaction struct {
	ID            string
	TxHash        string
	Type          TransactionType
	Status        TransactionStatus
	FromWallet    string
	ToWallet      string
	Asset         string
	Amount        decimal.Decimal
	Fee           decimal.Decimal
	FeeBps        int
	TenantID      *string
	CreatedAt     time.Time
	ReconciledAt  *time.Time
	RequeueCount  int
}

func (t *Transaction) NetAmount() decimal.Decimal {
	return t.Amount.Sub(t.Fee)
}
