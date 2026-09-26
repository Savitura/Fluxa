package wallet

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/assets"
	"github.com/fluxa/fluxa/internal/crypto"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	horizonclient "github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/txnbuild"
)

type TenantGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Tenant, error)
}

type FXRateGetter interface {
	GetRates(ctx context.Context, from, to string) (*domain.RateResponse, error)
}

type Balance struct {
	AssetCode     string `json:"asset_code"`
	Issuer        string `json:"issuer"`
	Balance       string `json:"balance"`
	USDEquivalent string `json:"usd_equivalent,omitempty"`
}

type Service interface {
	// CreateWallet provisions a wallet. Contract wallets require the owner's
	// public key; the custodial adapter generates its own keypair and ignores it.
	CreateWallet(ctx context.Context, ownerPublicKey ...string) (*domain.Wallet, error)
	GetBalances(ctx context.Context, walletID string, includeFX ...string) ([]Balance, error)
	AddTrustline(ctx context.Context, walletID, assetCode, issuer, limit string) (string, error)
	// ExecuteTransfer moves an asset out of the wallet and returns the
	// transaction hash. Custodial wallets use a classic Stellar payment;
	// contract wallets invoke execute_payment, which enforces the on-chain
	// spending limit and time-lock.
	ExecuteTransfer(ctx context.Context, walletID, destination, assetCode, issuer string, amount decimal.Decimal, memo string) (string, error)
	WithSigner(signer stellar.Signer) Service
	WithFXService(fxSvc FXRateGetter) Service
	WithIssuers(usdcIssuer, eurcIssuer string) Service
}

type service struct {
	repo          Repository
	stellar       stellar.Client
	signer        stellar.Signer
	masterKey     []byte
	tenantRepo    TenantGetter
	assetRegistry *assets.Registry
	fxSvc         FXRateGetter
	usdcIssuer    string
	eurcIssuer    string
}

func NewService(repo Repository, stellarClient stellar.Client, masterKey []byte, tenantRepo ...TenantGetter) Service {
	s := &service{
		repo:          repo,
		stellar:       stellarClient,
		masterKey:     masterKey,
		assetRegistry: assets.NewRegistry("", ""),
	}
	if len(tenantRepo) > 0 {
		s.tenantRepo = tenantRepo[0]
	}
	return s
}

func (s *service) WithSigner(signer stellar.Signer) Service {
	s.signer = signer
	return s
}

func (s *service) WithFXService(fxSvc FXRateGetter) Service {
	s.fxSvc = fxSvc
	return s
}

func (s *service) WithIssuers(usdcIssuer, eurcIssuer string) Service {
	s.usdcIssuer = usdcIssuer
	s.eurcIssuer = eurcIssuer
	s.assetRegistry = assets.NewRegistry(usdcIssuer, eurcIssuer)
	return s
}

// CreateWallet generates and encrypts a fresh keypair. ownerPublicKey is
// accepted for interface parity with the contract adapter and ignored here.
func (s *service) CreateWallet(ctx context.Context, ownerPublicKey ...string) (*domain.Wallet, error) {
	tenantID := tenant.IDFromContext(ctx)
	if tenantID != "" && s.tenantRepo != nil {
		t, err := s.tenantRepo.GetByID(ctx, tenantID)
		if err == nil && t != nil {
			limit := t.GetWalletLimit()
			count, err := s.repo.CountByTenant(ctx, tenantID)
			if err == nil && count >= limit {
				return nil, domain.ErrWalletLimitReached
			}
		}
	}

	pubKey, secretKey, err := stellar.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("generate keypair: %w", err)
	}

	encryptedBytes, err := crypto.Encrypt([]byte(secretKey), s.masterKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt secret: %w", err)
	}

	w := &domain.Wallet{
		ID:              uuid.New().String(),
		PublicKey:       pubKey,
		EncryptedSecret: hex.EncodeToString(encryptedBytes),
		CustodyType:     domain.CustodyCustodial,
		CreatedAt:       time.Now().UTC(),
	}

	if err := s.repo.Create(ctx, w); err != nil {
		return nil, fmt.Errorf("persist wallet: %w", err)
	}

	return w, nil
}

func (s *service) GetBalances(ctx context.Context, walletID string, includeFX ...string) ([]Balance, error) {
	w, err := s.repo.GetByID(ctx, walletID)
	if err != nil {
		return nil, err
	}

	var balances []Balance

	acct, err := s.stellar.LoadAccount(w.PublicKey)
	if err != nil {
		hErr, ok := err.(*horizonclient.Error)
		if ok && hErr.Response.Status == "404" {
			// Account not yet funded on Stellar — return empty balances
			return []Balance{}, nil
		}

		// Fallback to DB cached balances
		cached, dbErr := s.repo.GetBalances(ctx, walletID)
		if dbErr != nil || len(cached) == 0 {
			return nil, fmt.Errorf("load account from horizon: %w", err)
		}
		balances = make([]Balance, len(cached))
		for i, rec := range cached {
			balances[i] = Balance{
				AssetCode: rec.AssetCode,
				Issuer:    rec.Issuer,
				Balance:   rec.Balance,
			}
		}
	} else {
		balances = make([]Balance, 0, len(acct.Balances))
		for _, b := range acct.Balances {
			code := b.Code
			if code == "" {
				code = "XLM"
			}
			amt, _ := decimal.NewFromString(b.Balance)
			_ = s.repo.UpsertBalance(ctx, walletID, code, b.Issuer, amt)
			balances = append(balances, Balance{
				AssetCode: code,
				Issuer:    b.Issuer,
				Balance:   b.Balance,
			})
		}
	}

	targetFX := ""
	if len(includeFX) > 0 && includeFX[0] != "" {
		targetFX = includeFX[0]
	}

	if targetFX != "" {
		for i := range balances {
			amt, err := decimal.NewFromString(balances[i].Balance)
			if err != nil || amt.IsZero() {
				balances[i].USDEquivalent = "0.00"
				continue
			}

			if balances[i].AssetCode == targetFX || (balances[i].AssetCode == "USDC" && targetFX == "USD") {
				balances[i].USDEquivalent = amt.StringFixed(2)
			} else if s.fxSvc != nil {
				rateResp, err := s.fxSvc.GetRates(ctx, balances[i].AssetCode, targetFX)
				if err == nil && rateResp != nil {
					usdVal := amt.Mul(rateResp.Rate)
					balances[i].USDEquivalent = usdVal.StringFixed(2)
				} else {
					balances[i].USDEquivalent = "0.00"
				}
			} else {
				balances[i].USDEquivalent = "0.00"
			}
		}
	}

	return balances, nil
}

// ExecuteTransfer submits a classic Stellar payment signed with the wallet's
// stored secret. Custodial wallets carry no on-chain spending policy, so the
// only limits applied are Stellar's own.
func (s *service) ExecuteTransfer(
	ctx context.Context,
	walletID, destination, assetCode, issuer string,
	amount decimal.Decimal,
	memo string,
) (string, error) {
	w, err := s.repo.GetByID(ctx, walletID)
	if err != nil {
		return "", err
	}

	asset, err := s.resolveAsset(assetCode, issuer)
	if err != nil {
		return "", err
	}

	acct, err := s.stellar.LoadAccount(w.PublicKey)
	if err != nil {
		return "", fmt.Errorf("load account: %w", err)
	}

	txParams := txnbuild.TransactionParams{
		SourceAccount:        &acct,
		IncrementSequenceNum: true,
		Operations: []txnbuild.Operation{&txnbuild.Payment{
			Destination: destination,
			Amount:      amount.String(),
			Asset:       asset,
		}},
		BaseFee:       txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(30)},
	}
	if memo != "" {
		txParams.Memo = txnbuild.MemoText(memo)
	}

	stellarTx, err := txnbuild.NewTransaction(txParams)
	if err != nil {
		return "", fmt.Errorf("build payment transaction: %w", err)
	}

	signedTx, err := s.sign(stellarTx, w.EncryptedSecret)
	if err != nil {
		return "", fmt.Errorf("sign payment transaction: %w", err)
	}

	resp, err := s.stellar.SubmitTransaction(signedTx)
	if err != nil {
		return "", fmt.Errorf("submit payment to stellar: %w", err)
	}

	return resp.Hash, nil
}

// resolveAsset fills in a known issuer when the caller omitted one.
func (s *service) resolveAsset(assetCode, issuer string) (txnbuild.Asset, error) {
	if assetCode == "XLM" || assetCode == "" {
		return txnbuild.NativeAsset{}, nil
	}

	if issuer == "" {
		if a, ok := s.assetRegistry.Get(assetCode); ok && a.Issuer != "" {
			issuer = a.Issuer
		} else if assetCode == "USDC" {
			issuer = s.usdcIssuer
		} else if assetCode == "EURC" {
			issuer = s.eurcIssuer
		}
	}
	if issuer == "" {
		return nil, fmt.Errorf("%w: missing issuer for asset %s", domain.ErrInvalidAsset, assetCode)
	}

	return txnbuild.CreditAsset{Code: assetCode, Issuer: issuer}, nil
}

func (s *service) sign(tx *txnbuild.Transaction, encryptedSecret string) (*txnbuild.Transaction, error) {
	if s.signer != nil {
		return s.signer.Sign(tx, encryptedSecret)
	}
	return stellar.NewEnvSigner(s.masterKey, "testnet").Sign(tx, encryptedSecret)
}

func (s *service) AddTrustline(ctx context.Context, walletID, assetCode, issuer, limit string) (string, error) {
	if assetCode == "XLM" {
		return "", fmt.Errorf("%w: XLM is native and does not require a trustline", domain.ErrInvalidAsset)
	}

	if issuer == "" {
		if a, ok := s.assetRegistry.Get(assetCode); ok && a.Issuer != "" {
			issuer = a.Issuer
		} else if assetCode == "USDC" {
			issuer = s.usdcIssuer
		} else if assetCode == "EURC" {
			issuer = s.eurcIssuer
		}
	}

	if issuer == "" {
		return "", fmt.Errorf("%w: missing issuer for asset %s", domain.ErrInvalidAsset, assetCode)
	}

	w, err := s.repo.GetByID(ctx, walletID)
	if err != nil {
		return "", err
	}

	acct, err := s.stellar.LoadAccount(w.PublicKey)
	if err != nil {
		return "", fmt.Errorf("load account: %w", err)
	}

	ctAsset, err := txnbuild.CreditAsset{Code: assetCode, Issuer: issuer}.ToChangeTrustAsset()
	if err != nil {
		return "", fmt.Errorf("build change trust asset: %w", err)
	}
	if limit == "" {
		limit = txnbuild.MaxTrustlineLimit
	}

	op := &txnbuild.ChangeTrust{
		Line:  ctAsset,
		Limit: limit,
	}

	txParams := txnbuild.TransactionParams{
		SourceAccount:        &acct,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(30)},
	}

	stellarTx, err := txnbuild.NewTransaction(txParams)
	if err != nil {
		return "", fmt.Errorf("build change trust transaction: %w", err)
	}

	signedTx, err := s.sign(stellarTx, w.EncryptedSecret)
	if err != nil {
		return "", fmt.Errorf("sign trustline transaction: %w", err)
	}

	resp, err := s.stellar.SubmitTransaction(signedTx)
	if err != nil {
		return "", fmt.Errorf("submit trustline to stellar: %w", err)
	}

	_ = s.repo.UpsertBalance(ctx, walletID, assetCode, issuer, decimal.Zero)

	return resp.Hash, nil
}
