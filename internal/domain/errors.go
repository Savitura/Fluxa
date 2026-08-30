package domain

import "errors"

var (
	ErrWalletNotFound               = errors.New("wallet not found")
	ErrTransactionNotFound          = errors.New("transaction not found")
	ErrInsufficientBalance          = errors.New("insufficient balance")
	ErrStellarSubmission            = errors.New("stellar transaction submission failed")
	ErrDecryptionFailed             = errors.New("secret key decryption failed")
	ErrSlippageExceeded             = errors.New("slippage tolerance exceeded")
	ErrInvalidAsset                 = errors.New("invalid or unsupported asset")
	ErrSelfTransfer                 = errors.New("source and destination wallets must differ")
	ErrFeeScheduleNotFound          = errors.New("fee schedule not found")
	ErrReconciliationFailed         = errors.New("reconciliation check failed")
	ErrWebhookNotFound              = errors.New("webhook endpoint not found")
	ErrWebhookDeliveryNotFound      = errors.New("webhook delivery not found")
	ErrQuoteExpired                 = errors.New("quote expired")
	ErrQuoteAlreadyUsed             = errors.New("quote already used")
	ErrQuoteOwnershipMismatch       = errors.New("quote does not belong to this tenant")
	ErrInvalidQuoteAmount           = errors.New("quote amount must be positive")
	ErrBatchNotFound                = errors.New("batch not found")
	ErrBatchTooLarge                = errors.New("batch cannot contain more than 100 transfers")
	ErrBatchEmpty                   = errors.New("batch must contain at least one transfer")
	ErrScheduleNotFound             = errors.New("schedule not found")
	ErrUserNotFound                 = errors.New("user not found")
	ErrUserAlreadyExists            = errors.New("user with this email already exists")
	ErrInvalidCredentials           = errors.New("invalid email or password")
	ErrOrgMemberNotFound            = errors.New("organization member not found")
	ErrInviteNotFound               = errors.New("organization invite not found or expired")
	ErrForbidden                    = errors.New("insufficient permissions")
	ErrWalletLimitReached           = errors.New("wallet creation limit reached for account type")
	ErrTransferLimitReached         = errors.New("monthly transfer limit reached for account type")
	ErrWebhookLimitReached          = errors.New("webhook registration limit reached for account type")
	ErrInsufficientSweepableBalance = errors.New("sweep amount exceeds sweepable balance")
	ErrTreasuryConfigNotFound       = errors.New("treasury config not found for asset")
	ErrConcurrentUpdate            = errors.New("concurrent update: expected row was not modified")
	ErrSubPrecisionAmount         = errors.New("amount has more precision than the Stellar asset supports")

	ErrOwnerKeyRequired          = errors.New("owner public key is required to create a contract wallet")
	ErrNotContractWallet         = errors.New("wallet is not a contract wallet")
	ErrNoContractSigner          = errors.New("no signer configured for contract wallet invocations")
	ErrContractWasmNotConfigured = errors.New("contract wallet wasm hash is not configured")
	ErrTrustlineNotApplicable    = errors.New("contract wallets do not use trustlines")

	ErrTransferBlockedSanctions = errors.New("transfer blocked: destination matches a sanctions list entry")
	ErrComplianceReviewNotFound = errors.New("compliance review not found")
	ErrReviewNotPending         = errors.New("compliance review has already been decided")
)

type ErrNoTrustline struct {
	Asset string
}

func (e *ErrNoTrustline) Error() string {
	return "Source wallet has no trustline for " + e.Asset
}

func NewErrNoTrustline(asset string) error {
	return &ErrNoTrustline{Asset: asset}
}
