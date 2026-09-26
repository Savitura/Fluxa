// Package treasury monitors the platform fee wallet, computes how much of
// each asset can safely be moved to cold storage without dipping into
// Stellar's network reserve requirements, and executes/audits sweeps.
package treasury

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/keypair"
	stellarnet "github.com/stellar/go/network"
	"github.com/stellar/go/txnbuild"
)

// baseReserve is Stellar's per-subentry minimum balance requirement (fixed
// network-wide constant, currently 0.5 XLM). A funded account additionally
// always needs 2*baseReserve just to exist.
var baseReserve = decimal.RequireFromString("0.5")

// AssetBalance is the fee wallet's live balance for one asset, with its USD
// equivalent when a rate is available.
type AssetBalance struct {
	Asset         string          `json:"asset"`
	Balance       decimal.Decimal `json:"balance"`
	USDEquivalent decimal.Decimal `json:"usd_equivalent"`
}

// ReserveBreakdown is the full accounting behind GetReserveRequirement.
type ReserveBreakdown struct {
	WalletCount       int             `json:"wallet_count"`
	TrustlineCount    int             `json:"trustline_count"`
	OfferCount        int             `json:"open_offers_count"`
	TotalXLMRequired  decimal.Decimal `json:"total_xlm_required"`
	CurrentXLMBalance decimal.Decimal `json:"current_xlm_balance"`
	Surplus           decimal.Decimal `json:"surplus"` // negative means deficit
}

// FXRates resolves a spot rate between two assets. fx.Service already
// satisfies this (fx.RateResponse is a type alias for domain.RateResponse).
type FXRates interface {
	GetRates(ctx context.Context, from, to string) (*domain.RateResponse, error)
}

type Service interface {
	GetBalances(ctx context.Context) ([]AssetBalance, error)
	GetReserveRequirement(ctx context.Context) (decimal.Decimal, error)
	GetReserveBreakdown(ctx context.Context) (*ReserveBreakdown, error)
	GetSweepableAmount(ctx context.Context, asset string) (decimal.Decimal, error)
	// ExecuteSweep validates amount against the current sweepable balance,
	// then builds, signs, and submits a payment from the fee wallet to
	// destination. It always writes a sweep_log record ΓÇö including a
	// zero-amount audit row when amount is zero ΓÇö and returns the Stellar
	// tx hash (empty for a zero sweep).
	ExecuteSweep(ctx context.Context, asset string, amount decimal.Decimal, destination, triggeredBy string) (string, error)
	GetConfig(ctx context.Context) ([]*Config, error)
	UpdateConfig(ctx context.Context, cfg *Config) error
	ListSweeps(ctx context.Context, limit, offset int) ([]*SweepLog, error)
}

type service struct {
	repo              Repository
	stellar           stellar.Client
	fxRates           FXRates
	webhookSvc        webhook.Service
	feeWallet         string
	network           string
	treasurySecretKey string
	usdcIssuer        string
	eurcIssuer        string
}

func NewService(
	repo Repository,
	stellarClient stellar.Client,
	fxRates FXRates,
	webhookSvc webhook.Service,
	feeWallet, network, treasurySecretKey, usdcIssuer, eurcIssuer string,
) Service {
	return &service{
		repo:              repo,
		stellar:           stellarClient,
		fxRates:           fxRates,
		webhookSvc:        webhookSvc,
		feeWallet:         feeWallet,
		network:           network,
		treasurySecretKey: treasurySecretKey,
		usdcIssuer:        usdcIssuer,
		eurcIssuer:        eurcIssuer,
	}
}

func (s *service) GetBalances(ctx context.Context) ([]AssetBalance, error) {
	if s.feeWallet == "" {
		return nil, fmt.Errorf("PLATFORM_FEE_WALLET_PUBLIC_KEY is not configured")
	}

	acct, err := stellar.LoadAccountWithContext(ctx, s.stellar, s.feeWallet)
	if err != nil {
		return nil, fmt.Errorf("load fee wallet account: %w", err)
	}

	balances := make([]AssetBalance, 0, len(acct.Balances))
	for _, b := range acct.Balances {
		code := b.Code
		if code == "" {
			code = "XLM"
		}
		amt, err := decimal.NewFromString(b.Balance)
		if err != nil {
			continue
		}

		usd := decimal.Zero
		switch {
		case code == "USDC":
			usd = amt
		case s.fxRates != nil:
			if rate, err := s.fxRates.GetRates(ctx, code, "USDC"); err == nil {
				usd = amt.Mul(rate.Rate)
			}
			// No provider for this pair (e.g. XLM today) ΓÇö leave USD
			// equivalent at zero rather than failing the whole call.
		}

		balances = append(balances, AssetBalance{Asset: code, Balance: amt, USDEquivalent: usd})
	}
	return balances, nil
}

// GetReserveBreakdown sums, across every wallet Fluxa custodies, the XLM
// Stellar's protocol requires each account to keep locked up: a fixed
// 2*baseReserve per account plus baseReserve per trustline and per open
// offer (Stellar "subentries"). This is a platform-wide obligation, not
// specific to the fee wallet ΓÇö it's what determines how much of the fee
// wallet's own XLM is actually free to sweep.
func (s *service) GetReserveBreakdown(ctx context.Context) (*ReserveBreakdown, error) {
	pubKeys, err := s.repo.ListWalletPublicKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list wallet public keys: %w", err)
	}

	trustlines, offers := 0, 0
	for _, pk := range pubKeys {
		acct, err := stellar.LoadAccountWithContext(ctx, s.stellar, pk)
		if err != nil {
			// Not-yet-funded or unreachable wallet ΓÇö skip rather than fail
			// the whole reserve calculation over one bad account.
			continue
		}
		for _, b := range acct.Balances {
			if b.Code != "" {
				trustlines++
			}
		}
		if offerList, err := stellar.OffersWithContext(ctx, s.stellar, pk, 200); err == nil {
			offers += len(offerList)
		}
	}

	perAccountReserve := baseReserve.Mul(decimal.NewFromInt(2))
	total := perAccountReserve.Mul(decimal.NewFromInt(int64(len(pubKeys)))).
		Add(baseReserve.Mul(decimal.NewFromInt(int64(trustlines)))).
		Add(baseReserve.Mul(decimal.NewFromInt(int64(offers))))

	current := decimal.Zero
	if s.feeWallet != "" {
		if acct, err := stellar.LoadAccountWithContext(ctx, s.stellar, s.feeWallet); err == nil {
			for _, b := range acct.Balances {
				if b.Code == "" {
					if amt, err := decimal.NewFromString(b.Balance); err == nil {
						current = amt
					}
				}
			}
		}
	}

	return &ReserveBreakdown{
		WalletCount:       len(pubKeys),
		TrustlineCount:    trustlines,
		OfferCount:        offers,
		TotalXLMRequired:  total,
		CurrentXLMBalance: current,
		Surplus:           current.Sub(total),
	}, nil
}

func (s *service) GetReserveRequirement(ctx context.Context) (decimal.Decimal, error) {
	breakdown, err := s.GetReserveBreakdown(ctx)
	if err != nil {
		return decimal.Zero, err
	}
	return breakdown.TotalXLMRequired, nil
}

// GetSweepableAmount returns balance - (reserve_requirement + min_operating_buffer),
// floored at zero. The reserve requirement only applies to XLM ΓÇö it's a
// Stellar-network minimum-balance concept that doesn't exist for other assets.
func (s *service) GetSweepableAmount(ctx context.Context, asset string) (decimal.Decimal, error) {
	cfg, err := s.repo.GetConfig(ctx, asset)
	if err != nil {
		return decimal.Zero, err
	}

	balances, err := s.GetBalances(ctx)
	if err != nil {
		return decimal.Zero, err
	}

	var balance decimal.Decimal
	found := false
	for _, b := range balances {
		if b.Asset == asset {
			balance = b.Balance
			found = true
			break
		}
	}
	if !found {
		return decimal.Zero, nil
	}

	reserve := decimal.Zero
	if asset == "XLM" {
		reserve, err = s.GetReserveRequirement(ctx)
		if err != nil {
			return decimal.Zero, err
		}
	}

	sweepable := balance.Sub(reserve).Sub(cfg.MinOperatingBuffer)
	if sweepable.IsNegative() {
		return decimal.Zero, nil
	}
	return sweepable, nil
}

func (s *service) ExecuteSweep(ctx context.Context, asset string, amount decimal.Decimal, destination, triggeredBy string) (string, error) {
	sweepable, err := s.GetSweepableAmount(ctx, asset)
	if err != nil {
		return "", err
	}
	if amount.GreaterThan(sweepable) {
		return "", domain.ErrInsufficientSweepableBalance
	}

	if amount.IsZero() {
		if err := s.repo.RecordSweep(ctx, &SweepLog{
			ID:          uuid.New().String(),
			Asset:       asset,
			Amount:      decimal.Zero,
			Destination: destination,
			TxHash:      "",
			TriggeredBy: triggeredBy,
			SweptAt:     time.Now().UTC(),
		}); err != nil {
			return "", fmt.Errorf("record zero sweep: %w", err)
		}
		return "", nil
	}

	if s.treasurySecretKey == "" {
		return "", fmt.Errorf("TREASURY_SECRET_KEY is not configured")
	}
	if destination == "" {
		return "", fmt.Errorf("destination address is required")
	}

	kp, err := keypair.ParseFull(s.treasurySecretKey)
	if err != nil {
		return "", fmt.Errorf("parse treasury secret key: %w", err)
	}

	srcAccount, err := stellar.LoadAccountWithContext(ctx, s.stellar, s.feeWallet)
	if err != nil {
		return "", fmt.Errorf("load fee wallet account: %w", err)
	}

	stellarTx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &srcAccount,
		IncrementSequenceNum: true,
		Operations: []txnbuild.Operation{
			&txnbuild.Payment{
				Destination: destination,
				Asset:       s.buildAsset(asset),
				Amount:      amount.StringFixed(7),
			},
		},
		BaseFee: txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{
			TimeBounds: txnbuild.NewTimeout(30),
		},
	})
	if err != nil {
		return "", fmt.Errorf("build sweep transaction: %w", err)
	}

	stellarTx, err = stellarTx.Sign(s.networkPassphrase(), kp)
	if err != nil {
		return "", fmt.Errorf("sign sweep transaction: %w", err)
	}

	resp, err := stellar.SubmitTransactionWithContext(ctx, s.stellar, stellarTx)
	if err != nil {
		return "", fmt.Errorf("submit sweep transaction: %w", err)
	}

	if err := s.repo.RecordSweep(ctx, &SweepLog{
		ID:          uuid.New().String(),
		Asset:       asset,
		Amount:      amount,
		Destination: destination,
		TxHash:      resp.Hash,
		TriggeredBy: triggeredBy,
		SweptAt:     time.Now().UTC(),
	}); err != nil {
		log.Error().Err(err).Str("tx_hash", resp.Hash).Msg("treasury: failed to record sweep log")
	}

	if s.webhookSvc != nil {
		payload := map[string]interface{}{
			"asset":        asset,
			"amount":       amount.StringFixed(7),
			"destination":  destination,
			"tx_hash":      resp.Hash,
			"triggered_by": triggeredBy,
			"swept_at":     time.Now().UTC().Format(time.RFC3339),
		}
		if err := s.webhookSvc.Dispatch(ctx, domain.EventTreasurySweepCompleted, payload); err != nil {
			log.Error().Err(err).Msg("treasury: failed to dispatch treasury.sweep_completed webhook")
		}
	}

	return resp.Hash, nil
}

func (s *service) GetConfig(ctx context.Context) ([]*Config, error) {
	return s.repo.ListConfig(ctx)
}

func (s *service) UpdateConfig(ctx context.Context, cfg *Config) error {
	return s.repo.UpdateConfig(ctx, cfg)
}

func (s *service) ListSweeps(ctx context.Context, limit, offset int) ([]*SweepLog, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.ListSweeps(ctx, limit, offset)
}

func (s *service) buildAsset(code string) txnbuild.Asset {
	if code == "XLM" {
		return txnbuild.NativeAsset{}
	}
	issuer := ""
	switch code {
	case "USDC":
		issuer = s.usdcIssuer
	case "EURC":
		issuer = s.eurcIssuer
	}
	return txnbuild.CreditAsset{Code: code, Issuer: issuer}
}

func (s *service) networkPassphrase() string {
	if s.network == "mainnet" || s.network == "public" {
		return stellarnet.PublicNetworkPassphrase
	}
	return stellarnet.TestNetworkPassphrase
}
