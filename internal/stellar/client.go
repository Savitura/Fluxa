package stellar

import (
	"context"
	"fmt"

	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
)

// Client is the interface Fluxa uses to interact with Stellar/Horizon.
type Client interface {
	LoadAccount(accountID string) (horizon.Account, error)
	SubmitTransaction(tx *txnbuild.Transaction) (horizon.Transaction, error)
	FindPathsStrict(sourceAccount, destAsset, destIssuer, destAmount string) ([]horizon.Path, error)
	TransactionDetail(hash string) (horizon.Transaction, error)
	OperationsForTransaction(hash string) ([]operations.Operation, error)
	// Payments returns a page of payment operations for an account, starting
	// strictly after cursor (empty cursor starts from the account's first payment).
	Payments(accountID, cursor string, limit uint) ([]operations.Operation, error)
	// StreamPayments streams payment operations for an account in real time,
	// starting strictly after cursor (empty cursor streams only new payments).
	// It blocks until ctx is canceled or the stream errors.
	StreamPayments(ctx context.Context, accountID, cursor string, handler func(operations.Operation) error) error
}

type horizonClient struct {
	inner   *horizonclient.Client
	network string
}

func NewClient(horizonURL, network string) Client {
	return &horizonClient{
		inner:   &horizonclient.Client{HorizonURL: horizonURL},
		network: network,
	}
}

func (c *horizonClient) LoadAccount(accountID string) (horizon.Account, error) {
	acct, err := c.inner.AccountDetail(horizonclient.AccountRequest{AccountID: accountID})
	if err != nil {
		return horizon.Account{}, fmt.Errorf("load account %s: %w", accountID, err)
	}
	return acct, nil
}

func (c *horizonClient) SubmitTransaction(tx *txnbuild.Transaction) (horizon.Transaction, error) {
	resp, err := c.inner.SubmitTransaction(tx)
	if err != nil {
		return horizon.Transaction{}, fmt.Errorf("submit transaction: %w", err)
	}
	return resp, nil
}

func (c *horizonClient) TransactionDetail(hash string) (horizon.Transaction, error) {
	tx, err := c.inner.TransactionDetail(hash)
	if err != nil {
		return horizon.Transaction{}, fmt.Errorf("transaction detail: %w", err)
	}
	return tx, nil
}

func (c *horizonClient) OperationsForTransaction(hash string) ([]operations.Operation, error) {
	page, err := c.inner.Operations(horizonclient.OperationRequest{ForTransaction: hash})
	if err != nil {
		return nil, fmt.Errorf("operations for transaction: %w", err)
	}
	return page.Embedded.Records, nil
}

func (c *horizonClient) Payments(accountID, cursor string, limit uint) ([]operations.Operation, error) {
	page, err := c.inner.Payments(horizonclient.OperationRequest{
		ForAccount: accountID,
		Cursor:     cursor,
		Limit:      limit,
		Order:      horizonclient.OrderAsc,
	})
	if err != nil {
		return nil, fmt.Errorf("payments for account %s: %w", accountID, err)
	}
	return page.Embedded.Records, nil
}

func (c *horizonClient) StreamPayments(ctx context.Context, accountID, cursor string, handler func(operations.Operation) error) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var handlerErr error
	err := c.inner.StreamPayments(streamCtx, horizonclient.OperationRequest{
		ForAccount: accountID,
		Cursor:     cursor,
	}, func(op operations.Operation) {
		if handlerErr != nil {
			return
		}
		if err := handler(op); err != nil {
			handlerErr = err
			cancel() // stop the stream promptly; don't process further events
		}
	})
	if handlerErr != nil {
		return handlerErr
	}
	if err != nil {
		return fmt.Errorf("stream payments for account %s: %w", accountID, err)
	}
	return nil
}

func (c *horizonClient) FindPathsStrict(sourceAccount, destAsset, destIssuer, destAmount string) ([]horizon.Path, error) {
	req := horizonclient.PathsRequest{
		DestinationAccount:     sourceAccount,
		DestinationAssetType:   horizonclient.AssetType4,
		DestinationAssetCode:   destAsset,
		DestinationAssetIssuer: destIssuer,
		DestinationAmount:      destAmount,
	}
	paths, err := c.inner.Paths(req)
	if err != nil {
		return nil, fmt.Errorf("find paths: %w", err)
	}
	return paths.Embedded.Records, nil
}
