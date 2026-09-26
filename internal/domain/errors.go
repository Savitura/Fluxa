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
	ErrAmountOutOfLimits            = errors.New("amount outside allowed limits for asset")
	ErrBatchNotFound                = errors.New("batch not found")
	ErrBatchTooLarge                = errors.New("batch cannot contain more than 100 transfers")
	ErrBatchEmpty                   = errors.New("batch must contain at least one transfer")
	ErrScheduleNotFound             = errors.New("schedule not found")
	ErrScheduleRunNotFound          = errors.New("schedule run not found")
	ErrIncidentNotFound             = errors.New("incident not found")
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
	ErrConcurrentUpdate             = errors.New("concurrent update: expected row was not modified")
	ErrSubPrecisionAmount           = errors.New("amount has more precision than the Stellar asset supports")

	ErrOwnerKeyRequired          = errors.New("owner public key is required to create a contract wallet")
	ErrNotContractWallet         = errors.New("wallet is not a contract wallet")
	ErrNoContractSigner          = errors.New("no signer configured for contract wallet invocations")
	ErrContractWasmNotConfigured = errors.New("contract wallet wasm hash is not configured")
	ErrTrustlineNotApplicable    = errors.New("contract wallets do not use trustlines")

	ErrTransferBlockedSanctions = errors.New("transfer blocked: destination matches a sanctions list entry")
	ErrComplianceReviewNotFound = errors.New("compliance review not found")
	ErrReviewNotPending         = errors.New("compliance review has already been decided")

	// Claimable balance errors. The claim path distinguishes "you cannot claim
	// this yet" (predicate not satisfiable) from "this is no longer claimable"
	// (already claimed/expired) because the first is retryable and the second
	// is terminal.
	ErrClaimableBalanceNotFound   = errors.New("claimable balance not found")
	ErrClaimableBalanceNotPending = errors.New("claimable balance is no longer pending")
	ErrPredicateNotSatisfiable    = errors.New("claimant predicate is not currently satisfiable")
	ErrInvalidPredicate           = errors.New("invalid claim predicate")
	ErrNoClaimants                = errors.New("a claimable balance needs at least one claimant")
	ErrClaimantNotFound           = errors.New("the requested claimant is not part of this claimable balance")
	ErrClaimantNotCustodied       = errors.New("claimant account is not a wallet custodied by Fluxa")
	ErrSourceWalletRequired       = errors.New("a source wallet is required to fund a claimable balance")
	ErrSponsorNotCustodied        = errors.New("sponsor account is not a wallet custodied by Fluxa")
	ErrInvalidAmount              = errors.New("amount must be a positive number")
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
