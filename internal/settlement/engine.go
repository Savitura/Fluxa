package settlement

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/rs/zerolog/log"
	stellarnet "github.com/stellar/go/network"
	"github.com/stellar/go/txnbuild"
)

type Engine struct {
	txRepo     transfer.Repository
	walletRepo wallet.Repository
	stellar    stellar.Client
	signer     stellar.Signer
	network    string
}

func NewEngine(
	txRepo transfer.Repository,
	walletRepo wallet.Repository,
	stellarClient stellar.Client,
	signer stellar.Signer,
	network string,
) *Engine {
	return &Engine{
		txRepo:     txRepo,
		walletRepo: walletRepo,
		stellar:    stellarClient,
		signer:     signer,
		network:    network,
	}
}

func (e *Engine) SubmitTransfer(ctx context.Context, txID string) error {
	tx, err := e.txRepo.GetByID(ctx, txID)
	if err != nil {
		return fmt.Errorf("load transaction: %w", err)
	}

	if tx.Status != domain.StatusPending {
		log.Warn().Str("tx_id", txID).Str("status", string(tx.Status)).Msg("skipping non-pending transaction")
		return nil
	}

	srcWallet, err := e.walletRepo.GetByID(ctx, tx.FromWallet)
	if err != nil {
		return fmt.Errorf("load source wallet: %w", err)
	}

	srcAccount, err := e.stellar.LoadAccount(srcWallet.PublicKey)
	if err != nil {
		return fmt.Errorf("load stellar account: %w", err)
	}

	dstWallet, err := e.walletRepo.GetByID(ctx, tx.ToWallet)
	if err != nil {
		return fmt.Errorf("load destination wallet: %w", err)
	}

	asset := txnbuild.NativeAsset{}
	var txAsset txnbuild.Asset = asset
	if tx.Asset != "XLM" {
		txAsset = txnbuild.CreditAsset{Code: tx.Asset}
	}

	stellarTx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &srcAccount,
		IncrementSequenceNum: true,
		Operations: []txnbuild.Operation{
			&txnbuild.Payment{
				Destination: dstWallet.PublicKey,
				Asset:       txAsset,
				Amount:      tx.Amount.StringFixed(7),
			},
		},
		BaseFee:    txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{
			TimeBounds: txnbuild.NewTimeout(30),
		},
	})
	if err != nil {
		return fmt.Errorf("build transaction: %w", err)
	}

	encryptedSecret, err := hex.DecodeString(srcWallet.EncryptedSecret)
	if err != nil {
		return fmt.Errorf("decode encrypted secret: %w", err)
	}

	stellarTx, err = e.signer.Sign(stellarTx, string(encryptedSecret))
	if err != nil {
		return fmt.Errorf("sign transaction: %w", err)
	}

	resp, submitErr := e.submitWithRetry(ctx, stellarTx)

	if submitErr != nil {
		_ = e.txRepo.UpdateStatus(ctx, txID, domain.StatusFailed, "")
		return fmt.Errorf("submit to stellar: %w", submitErr)
	}

	if err := e.txRepo.UpdateStatus(ctx, txID, domain.StatusConfirmed, resp.Hash); err != nil {
		log.Error().Err(err).Str("tx_id", txID).Str("tx_hash", resp.Hash).Msg("failed to update confirmed status")
	}

	return nil
}

func (e *Engine) networkPassphrase() string {
	if e.network == "mainnet" || e.network == "public" {
		return stellarnet.PublicNetworkPassphrase
	}
	return stellarnet.TestNetworkPassphrase
}

func (e *Engine) submitWithRetry(ctx context.Context, tx *txnbuild.Transaction) (interface{ GetHash() string }, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}

		resp, err := e.stellar.SubmitTransaction(tx)
		if err == nil {
			return &horizonTxResp{hash: resp.Hash}, nil
		}

		lastErr = err
		if !isRetryable(err) {
			break
		}
	}
	return nil, errors.New("stellar submission failed after retries: " + lastErr.Error())
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Horizon 429 (rate limit) and 503 (service unavailable) are retryable
	errStr := err.Error()
	return contains(errStr, "429") || contains(errStr, "503") || contains(errStr, "timeout")
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

type horizonTxResp struct{ hash string }

func (r *horizonTxResp) GetHash() string { return r.hash }
