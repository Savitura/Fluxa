package stellar

import (
	"errors"
	"fmt"

	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/protocols/horizon"
)

// ErrClaimableBalanceNotFound means Horizon does not know the requested
// claimable balance — typically because it has already been claimed, clawed
// back, or never existed on this network.
var ErrClaimableBalanceNotFound = errors.New("claimable balance not found")

// ClaimableBalanceClient reads claimable balances from Horizon. It is declared
// separately from Client on purpose: Client is implemented by mocks across
// several packages, so widening it would break every existing implementer for
// no benefit.
type ClaimableBalanceClient interface {
	ClaimableBalance(id string) (horizon.ClaimableBalance, error)
	// ClaimableBalancesForAccount returns the balances this account can claim,
	// up to Horizon's page limit.
	ClaimableBalancesForAccount(accountID string, limit uint) ([]horizon.ClaimableBalance, error)
}

type horizonClaimableClient struct {
	inner *horizonclient.Client
}

func NewClaimableBalanceClient(horizonURL string) ClaimableBalanceClient {
	return &horizonClaimableClient{
		inner: &horizonclient.Client{HorizonURL: horizonURL},
	}
}

func (c *horizonClaimableClient) ClaimableBalance(id string) (horizon.ClaimableBalance, error) {
	cb, err := c.inner.ClaimableBalance(id)
	if err != nil {
		if horizonclient.IsNotFoundError(err) {
			return horizon.ClaimableBalance{}, fmt.Errorf("%w: %s", ErrClaimableBalanceNotFound, id)
		}
		return horizon.ClaimableBalance{}, fmt.Errorf("claimable balance %s: %w", id, err)
	}
	return cb, nil
}

func (c *horizonClaimableClient) ClaimableBalancesForAccount(accountID string, limit uint) ([]horizon.ClaimableBalance, error) {
	page, err := c.inner.ClaimableBalances(horizonclient.ClaimableBalanceRequest{
		Claimant: accountID,
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("claimable balances for account %s: %w", accountID, err)
	}
	return page.Embedded.Records, nil
}
