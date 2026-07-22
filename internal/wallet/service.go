package wallet

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/crypto"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	horizonclient "github.com/stellar/go/clients/horizonclient"
)

type TenantGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Tenant, error)
}

type Balance struct {
	AssetCode string `json:"asset_code"`
	Issuer    string `json:"issuer"`
	Balance   string `json:"balance"`
}

type Service interface {
	CreateWallet(ctx context.Context) (*domain.Wallet, error)
	GetBalances(ctx context.Context, walletID string) ([]Balance, error)
}

type service struct {
	repo       Repository
	stellar    stellar.Client
	masterKey  []byte
	tenantRepo TenantGetter
}

func NewService(repo Repository, stellarClient stellar.Client, masterKey []byte, tenantRepo ...TenantGetter) Service {
	s := &service{
		repo:      repo,
		stellar:   stellarClient,
		masterKey: masterKey,
	}
	if len(tenantRepo) > 0 {
		s.tenantRepo = tenantRepo[0]
	}
	return s
}

func (s *service) CreateWallet(ctx context.Context) (*domain.Wallet, error) {
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
		CreatedAt:       time.Now().UTC(),
	}

	if err := s.repo.Create(ctx, w); err != nil {
		return nil, fmt.Errorf("persist wallet: %w", err)
	}

	return w, nil
}

func (s *service) GetBalances(ctx context.Context, walletID string) ([]Balance, error) {
	w, err := s.repo.GetByID(ctx, walletID)
	if err != nil {
		return nil, err
	}

	acct, err := s.stellar.LoadAccount(w.PublicKey)
	if err != nil {
		hErr, ok := err.(*horizonclient.Error)
		if ok && hErr.Response.Status == "404" {
			// Account not yet funded on Stellar — return empty balances
			return []Balance{}, nil
		}
		return nil, fmt.Errorf("load account from horizon: %w", err)
	}

	balances := make([]Balance, 0, len(acct.Balances))
	for _, b := range acct.Balances {
		code := b.Code
		if code == "" {
			code = "XLM"
		}
		balances = append(balances, Balance{
			AssetCode: code,
			Issuer:    b.Issuer,
			Balance:   b.Balance,
		})
	}
	return balances, nil
}
