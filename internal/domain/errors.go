package domain

import "errors"

var (
	ErrWalletNotFound      = errors.New("wallet not found")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrStellarSubmission   = errors.New("stellar transaction submission failed")
	ErrDecryptionFailed    = errors.New("secret key decryption failed")
	ErrSlippageExceeded    = errors.New("slippage tolerance exceeded")
	ErrInvalidAsset        = errors.New("invalid or unsupported asset")
	ErrSelfTransfer        = errors.New("source and destination wallets must differ")
	ErrFeeScheduleNotFound = errors.New("fee schedule not found")
)
