package stellar

import (
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
