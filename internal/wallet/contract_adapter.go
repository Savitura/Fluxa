package wallet

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

// ContractDeployer deploys a new instance of the contract wallet WASM and
// initializes it. Deployment is a distinct concern from ordinary invocation:
// it uploads/creates a contract instance rather than calling an existing one.
type ContractDeployer interface {
	Deploy(ctx context.Context, owner string, params ContractWalletParams) (contractID string, err error)
}

// ContractWalletParams are the on-chain policy settings baked into a contract
// wallet at deployment time.
type ContractWalletParams struct {
	Guardians             []string
	RecoveryThreshold     uint32
	SpendingLimit         decimal.Decimal
	SpendingWindowSeconds uint64
}

// ContractState mirrors the contract's `get_state` return value.
type ContractState struct {
	Owner                 string   `json:"owner"`
	ContractID            string   `json:"contract_id"`
	Guardians             []string `json:"guardians"`
	RecoveryThreshold     uint32   `json:"recovery_threshold"`
	SpendingLimit         string   `json:"spending_limit"`
	SpendingWindowSeconds uint64   `json:"spending_window_seconds"`
	SpentInWindow         string   `json:"spent_in_window"`
	WindowStart           uint64   `json:"window_start"`
	TimeLockUntil         uint64   `json:"time_lock_until"`
	TimeLocked            bool     `json:"time_locked"`
	HasPendingRecovery    bool     `json:"has_pending_recovery"`
}

// SpendingStatus mirrors the contract's `get_spending_status` return value.
type SpendingStatus struct {
	Limit          string `json:"limit"`
	SpentInWindow  string `json:"spent_in_window"`
	Remaining      string `json:"remaining"`
	WindowResetsAt uint64 `json:"window_resets_at"`
}

// ContractService is the contract-wallet-only surface layered on top of the
// shared Service interface. Only the contract adapter implements it.
type ContractService interface {
	Service
	GetContractState(ctx context.Context, walletID string) (*ContractState, error)
	GetSpendingStatus(ctx context.Context, walletID string) (*SpendingStatus, error)
	AddGuardian(ctx context.Context, walletID, guardian string) (string, error)
	RemoveGuardian(ctx context.Context, walletID, guardian string) (string, error)
	SetTimeLock(ctx context.Context, walletID string, untilTimestamp uint64) (string, error)
}

// ContractWalletAdapter is the non-custodial WalletService implementation.
// Fluxa never stores a secret key for these wallets: the spending limit,
// time-lock and guardian recovery rules are enforced on-chain by the Soroban
// contract, and signing is delegated through the stellar.Signer boundary.
type ContractWalletAdapter struct {
	repo       Repository
	soroban    stellar.SorobanClient
	deployer   ContractDeployer
	signer     stellar.Signer
	tenantRepo TenantGetter
	assetSvc   AssetResolver
	defaults   ContractWalletParams
}

// AssetResolver maps an asset code onto the Stellar Asset Contract address
// that a Soroban contract must call to move it.
type AssetResolver interface {
	ContractAddress(assetCode, issuer string) (string, error)
}

func NewContractWalletAdapter(
	repo Repository,
	soroban stellar.SorobanClient,
	deployer ContractDeployer,
	assetSvc AssetResolver,
	defaults ContractWalletParams,
) *ContractWalletAdapter {
	return &ContractWalletAdapter{
		repo:     repo,
		soroban:  soroban,
		deployer: deployer,
		assetSvc: assetSvc,
		defaults: defaults,
	}
}

func (a *ContractWalletAdapter) WithSigner(signer stellar.Signer) Service {
	a.signer = signer
	return a
}

// WithFXService is a no-op: contract wallet balances are reported as raw
// token amounts, without FX conversion.
func (a *ContractWalletAdapter) WithFXService(fxSvc FXRateGetter) Service {
	return a
}

// WithIssuers is a no-op: asset issuers are resolved by the AssetResolver,
// which the adapter is constructed with.
func (a *ContractWalletAdapter) WithIssuers(usdcIssuer, eurcIssuer string) Service {
	return a
}

func (a *ContractWalletAdapter) WithTenantRepo(t TenantGetter) *ContractWalletAdapter {
	a.tenantRepo = t
	return a
}

// CreateWallet deploys a contract wallet owned by ownerPublicKey. No secret
// key is generated or persisted — the owner keeps their key on their device.
func (a *ContractWalletAdapter) CreateWallet(ctx context.Context, ownerPublicKey ...string) (*domain.Wallet, error) {
	if len(ownerPublicKey) == 0 || ownerPublicKey[0] == "" {
		return nil, domain.ErrOwnerKeyRequired
	}
	owner := ownerPublicKey[0]

	tenantID := tenant.IDFromContext(ctx)
	if tenantID != "" && a.tenantRepo != nil {
		t, err := a.tenantRepo.GetByID(ctx, tenantID)
		if err == nil && t != nil {
			count, err := a.repo.CountByTenant(ctx, tenantID)
			if err == nil && count >= t.GetWalletLimit() {
				return nil, domain.ErrWalletLimitReached
			}
		}
	}

	contractID, err := a.deployer.Deploy(ctx, owner, a.defaults)
	if err != nil {
		return nil, fmt.Errorf("deploy contract wallet: %w", err)
	}

	w := &domain.Wallet{
		ID:          uuid.New().String(),
		PublicKey:   owner,
		CustodyType: domain.CustodyContract,
		ContractID:  contractID,
		CreatedAt:   time.Now().UTC(),
	}

	if err := a.repo.Create(ctx, w); err != nil {
		return nil, fmt.Errorf("persist contract wallet: %w", err)
	}

	return w, nil
}

// GetBalances returns the balances recorded for the contract address. A
// Soroban contract holds tokens in contract storage rather than in a classic
// account, so these come from Fluxa's own records rather than Horizon, and
// includeFX is not applied.
func (a *ContractWalletAdapter) GetWalletForHandler(ctx context.Context, walletID string) (*domain.Wallet, error) {
	return a.repo.GetByID(ctx, walletID)
}

func (a *ContractWalletAdapter) GetBalances(ctx context.Context, walletID string, includeFX ...string) ([]Balance, error) {
	if _, err := a.contractWallet(ctx, walletID); err != nil {
		return nil, err
	}

	cached, err := a.repo.GetBalances(ctx, walletID)
	if err != nil {
		return nil, fmt.Errorf("get contract wallet balances: %w", err)
	}

	balances := make([]Balance, 0, len(cached))
	for _, rec := range cached {
		balances = append(balances, Balance{
			AssetCode: rec.AssetCode,
			Issuer:    rec.Issuer,
			Balance:   rec.Balance,
		})
	}

	return balances, nil
}

// AddTrustline is not applicable to contract wallets: a Soroban contract holds
// token balances in contract storage and needs no trustlines.
func (a *ContractWalletAdapter) AddTrustline(ctx context.Context, walletID, assetCode, issuer, limit string) (string, error) {
	return "", domain.ErrTrustlineNotApplicable
}

// ExecuteTransfer invokes execute_payment on the wallet contract. The spending
// limit and time-lock are enforced by the contract itself, so a rejected
// payment surfaces here as a failed simulation rather than a local check.
func (a *ContractWalletAdapter) ExecuteTransfer(
	ctx context.Context,
	walletID, destination, assetCode, issuer string,
	amount decimal.Decimal,
	memo string,
) (string, error) {
	w, err := a.repo.GetByID(ctx, walletID)
	if err != nil {
		return "", err
	}
	if w.ContractID == "" {
		return "", domain.ErrNotContractWallet
	}

	assetContract, err := a.assetSvc.ContractAddress(assetCode, issuer)
	if err != nil {
		return "", fmt.Errorf("resolve asset contract for %s: %w", assetCode, err)
	}

	destArg, err := stellar.AddressScVal(destination)
	if err != nil {
		return "", err
	}
	assetArg, err := stellar.ContractScVal(assetContract)
	if err != nil {
		return "", err
	}

	if err := validateStellarPrecision(amount); err != nil {
		return "", err
	}

	memoArg := stellar.VoidScVal()
	if memo != "" {
		memoArg = stellar.StringScVal(memo)
	}

	args := xdr.ScVec{destArg, assetArg, stellar.I128ScVal(toStroops(amount)), memoArg}

	return a.invoke(ctx, w, "execute_payment", args)
}

func (a *ContractWalletAdapter) AddGuardian(ctx context.Context, walletID, guardian string) (string, error) {
	return a.invokeAddressCall(ctx, walletID, "add_guardian", guardian)
}

func (a *ContractWalletAdapter) RemoveGuardian(ctx context.Context, walletID, guardian string) (string, error) {
	return a.invokeAddressCall(ctx, walletID, "remove_guardian", guardian)
}

func (a *ContractWalletAdapter) SetTimeLock(ctx context.Context, walletID string, untilTimestamp uint64) (string, error) {
	w, err := a.contractWallet(ctx, walletID)
	if err != nil {
		return "", err
	}
	return a.invoke(ctx, w, "set_time_lock", xdr.ScVec{stellar.U64ScVal(untilTimestamp)})
}

func (a *ContractWalletAdapter) GetContractState(ctx context.Context, walletID string) (*ContractState, error) {
	w, err := a.contractWallet(ctx, walletID)
	if err != nil {
		return nil, err
	}

	result, err := a.simulateRead(ctx, w, "get_state")
	if err != nil {
		return nil, err
	}

	state := &ContractState{ContractID: w.ContractID}

	if state.Owner, err = decodeAddressField(result, "owner"); err != nil {
		return nil, err
	}
	guardians, err := stellar.DecodeMapField(result, "guardians")
	if err != nil {
		return nil, err
	}
	if state.Guardians, err = stellar.DecodeAddressVec(guardians); err != nil {
		return nil, err
	}
	if state.RecoveryThreshold, err = decodeU32Field(result, "recovery_threshold"); err != nil {
		return nil, err
	}
	if state.SpendingLimit, err = decodeI128Field(result, "spending_limit"); err != nil {
		return nil, err
	}
	if state.SpendingWindowSeconds, err = decodeU64Field(result, "spending_window_seconds"); err != nil {
		return nil, err
	}
	if state.SpentInWindow, err = decodeI128Field(result, "spent_in_window"); err != nil {
		return nil, err
	}
	if state.WindowStart, err = decodeU64Field(result, "window_start"); err != nil {
		return nil, err
	}
	if state.TimeLockUntil, err = decodeU64Field(result, "time_lock_until"); err != nil {
		return nil, err
	}
	pending, err := stellar.DecodeMapField(result, "has_pending_recovery")
	if err != nil {
		return nil, err
	}
	if state.HasPendingRecovery, err = stellar.DecodeBool(pending); err != nil {
		return nil, err
	}

	state.TimeLocked = state.TimeLockUntil > uint64(time.Now().UTC().Unix())

	return state, nil
}

func (a *ContractWalletAdapter) GetSpendingStatus(ctx context.Context, walletID string) (*SpendingStatus, error) {
	w, err := a.contractWallet(ctx, walletID)
	if err != nil {
		return nil, err
	}

	result, err := a.simulateRead(ctx, w, "get_spending_status")
	if err != nil {
		return nil, err
	}

	status := &SpendingStatus{}
	if status.Limit, err = decodeI128Field(result, "limit"); err != nil {
		return nil, err
	}
	if status.SpentInWindow, err = decodeI128Field(result, "spent_in_window"); err != nil {
		return nil, err
	}
	if status.Remaining, err = decodeI128Field(result, "remaining"); err != nil {
		return nil, err
	}
	if status.WindowResetsAt, err = decodeU64Field(result, "window_resets_at"); err != nil {
		return nil, err
	}

	return status, nil
}

func (a *ContractWalletAdapter) contractWallet(ctx context.Context, walletID string) (*domain.Wallet, error) {
	w, err := a.repo.GetByID(ctx, walletID)
	if err != nil {
		return nil, err
	}
	if w.ContractID == "" {
		return nil, domain.ErrNotContractWallet
	}
	return w, nil
}

func (a *ContractWalletAdapter) invokeAddressCall(ctx context.Context, walletID, fn, address string) (string, error) {
	w, err := a.contractWallet(ctx, walletID)
	if err != nil {
		return "", err
	}
	arg, err := stellar.AddressScVal(address)
	if err != nil {
		return "", err
	}
	return a.invoke(ctx, w, fn, xdr.ScVec{arg})
}

// invoke prepares, signs and submits a state-changing contract call.
func (a *ContractWalletAdapter) invoke(ctx context.Context, w *domain.Wallet, fn string, args xdr.ScVec) (string, error) {
	op, err := a.buildInvocation(w, fn, args)
	if err != nil {
		return "", err
	}

	tx, err := a.soroban.PrepareInvocation(ctx, w.PublicKey, op)
	if err != nil {
		return "", fmt.Errorf("prepare %s invocation: %w", fn, err)
	}

	if a.signer == nil {
		return "", domain.ErrNoContractSigner
	}
	signed, err := a.signer.Sign(tx, w.EncryptedSecret)
	if err != nil {
		return "", fmt.Errorf("sign %s invocation: %w", fn, err)
	}

	hash, err := a.soroban.SubmitTransaction(ctx, signed)
	if err != nil {
		return "", fmt.Errorf("submit %s invocation: %w", fn, err)
	}

	return hash, nil
}

// simulateRead runs a read-only contract call through simulation, which costs
// nothing and touches no ledger state.
func (a *ContractWalletAdapter) simulateRead(ctx context.Context, w *domain.Wallet, fn string) (xdr.ScVal, error) {
	op, err := a.buildInvocation(w, fn, xdr.ScVec{})
	if err != nil {
		return xdr.ScVal{}, err
	}

	result, err := a.soroban.SimulateInvocation(ctx, w.PublicKey, op)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("simulate %s: %w", fn, err)
	}
	return result, nil
}

func (a *ContractWalletAdapter) buildInvocation(w *domain.Wallet, fn string, args xdr.ScVec) (*txnbuild.InvokeHostFunction, error) {
	contractID, err := stellar.ParseContractID(w.ContractID)
	if err != nil {
		return nil, err
	}

	return &txnbuild.InvokeHostFunction{
		HostFunction: xdr.HostFunction{
			Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
			InvokeContract: &xdr.InvokeContractArgs{
				ContractAddress: xdr.ScAddress{
					Type:       xdr.ScAddressTypeScAddressTypeContract,
					ContractId: &contractID,
				},
				FunctionName: xdr.ScSymbol(fn),
				Args:         args,
			},
		},
		SourceAccount: w.PublicKey,
	}, nil
}

// toStroops converts a decimal amount to the 7-decimal fixed point integer
// representation Stellar assets use on-chain.
func toStroops(amount decimal.Decimal) *big.Int {
	return amount.Shift(7).Truncate(0).BigInt()
}

// validateStellarPrecision rejects amounts whose decimal representation has
// more than 7 fractional digits. Stellar assets use 7-decimal fixed-point
// integers on-chain, so any finer precision would be silently truncated by
// toStroops — transferring less than requested or rounding to zero.
func validateStellarPrecision(amount decimal.Decimal) error {
	if amount.Exponent() < -7 {
		return domain.ErrSubPrecisionAmount
	}
	return nil
}

func decodeAddressField(v xdr.ScVal, field string) (string, error) {
	f, err := stellar.DecodeMapField(v, field)
	if err != nil {
		return "", err
	}
	return stellar.DecodeAddress(f)
}

func decodeU32Field(v xdr.ScVal, field string) (uint32, error) {
	f, err := stellar.DecodeMapField(v, field)
	if err != nil {
		return 0, err
	}
	return stellar.DecodeU32(f)
}

func decodeU64Field(v xdr.ScVal, field string) (uint64, error) {
	f, err := stellar.DecodeMapField(v, field)
	if err != nil {
		return 0, err
	}
	return stellar.DecodeU64(f)
}

// decodeI128Field renders i128 amounts back to their decimal string form.
func decodeI128Field(v xdr.ScVal, field string) (string, error) {
	f, err := stellar.DecodeMapField(v, field)
	if err != nil {
		return "", err
	}
	i, err := stellar.DecodeI128(f)
	if err != nil {
		return "", err
	}
	return decimal.NewFromBigInt(i, -7).String(), nil
}
