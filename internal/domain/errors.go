package domain

import "errors"

var (
	ErrWalletNotFound          = errors.New("wallet not found")
	ErrTransactionNotFound     = errors.New("transaction not found")
	ErrInsufficientBalance     = errors.New("insufficient balance")
	ErrStellarSubmission       = errors.New("stellar transaction submission failed")
	ErrDecryptionFailed        = errors.New("secret key decryption failed")
	ErrSlippageExceeded        = errors.New("slippage tolerance exceeded")
	ErrInvalidAsset            = errors.New("invalid or unsupported asset")
	ErrSelfTransfer            = errors.New("source and destination wallets must differ")
	ErrFeeScheduleNotFound     = errors.New("fee schedule not found")
	ErrReconciliationFailed    = errors.New("reconciliation check failed")
	ErrWebhookNotFound         = errors.New("webhook endpoint not found")
	ErrWebhookDeliveryNotFound = errors.New("webhook delivery not found")
	ErrQuoteExpired            = errors.New("quote expired")
	ErrQuoteAlreadyUsed        = errors.New("quote already used")
	ErrBatchNotFound           = errors.New("batch not found")
	ErrBatchTooLarge           = errors.New("batch cannot contain more than 100 transfers")
	ErrBatchEmpty              = errors.New("batch must contain at least one transfer")
	ErrScheduleNotFound        = errors.New("schedule not found")
)
