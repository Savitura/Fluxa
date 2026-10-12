package anchor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Sep6Client performs non-interactive deposits/withdrawals (SEP-6) against an
// anchor's TRANSFER_SERVER. It is not bound to a single anchor: every call
// takes the target anchor's transferServer URL, since a wallet may deal with
// several anchors concurrently.
type Sep6Client struct {
	httpClient *http.Client
}

func NewSep6Client(httpClient *http.Client) *Sep6Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Sep6Client{httpClient: httpClient}
}

// AssetOperationInfo describes one asset's deposit or withdraw support as
// reported by an anchor's GET /info.
type AssetOperationInfo struct {
	Enabled    bool                   `json:"enabled"`
	MinAmount  float64                `json:"min_amount,omitempty"`
	MaxAmount  float64                `json:"max_amount,omitempty"`
	FeeFixed   float64                `json:"fee_fixed,omitempty"`
	FeePercent float64                `json:"fee_percent,omitempty"`
	Fields     map[string]interface{} `json:"fields,omitempty"`
}

// AnchorInfo is the parsed response of an anchor's GET /info (SEP-6).
type AnchorInfo struct {
	Deposit  map[string]AssetOperationInfo `json:"deposit"`
	Withdraw map[string]AssetOperationInfo `json:"withdraw"`
}

// GetInfo fetches and parses an anchor's SEP-6 GET /info, describing which
// assets support deposit/withdrawal, their fees, limits and KYC fields.
func (c *Sep6Client) GetInfo(ctx context.Context, transferServer string) (*AnchorInfo, error) {
	var info AnchorInfo
	if err := c.doJSON(ctx, transferServer+"/info", "", &info); err != nil {
		return nil, fmt.Errorf("get sep-6 info: %w", err)
	}
	return &info, nil
}

// InstructionField is one field of the "instructions" object an anchor
// returns describing how to complete a bank transfer (e.g. bank account
// number, routing number).
type InstructionField struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

// DepositInstructions is the response to a SEP-6 GET /deposit request.
type DepositInstructions struct {
	ID           string                      `json:"id"`
	How          string                      `json:"how,omitempty"`
	ETA          int                         `json:"eta,omitempty"`
	MinAmount    float64                     `json:"min_amount,omitempty"`
	MaxAmount    float64                     `json:"max_amount,omitempty"`
	FeeFixed     float64                     `json:"fee_fixed,omitempty"`
	Instructions map[string]InstructionField `json:"instructions,omitempty"`
	ExtraInfo    map[string]interface{}      `json:"extra_info,omitempty"`
}

// InitiateDeposit calls an anchor's SEP-6 GET /deposit (a query, per spec —
// there is no request body) to begin a non-interactive fiat deposit that
// will credit account on-chain in asset.
func (c *Sep6Client) InitiateDeposit(ctx context.Context, transferServer, jwt, assetCode, account, amount, email string) (*DepositInstructions, error) {
	q := url.Values{}
	q.Set("asset_code", assetCode)
	q.Set("account", account)
	if amount != "" {
		q.Set("amount", amount)
	}
	if email != "" {
		q.Set("email_address", email)
	}

	var out DepositInstructions
	if err := c.doJSON(ctx, transferServer+"/deposit?"+q.Encode(), jwt, &out); err != nil {
		return nil, fmt.Errorf("initiate sep-6 deposit: %w", err)
	}
	return &out, nil
}

// WithdrawalResult is the response to a SEP-6 GET /withdraw request: the
// on-chain account/memo the client must pay to complete the withdrawal.
type WithdrawalResult struct {
	ID        string                 `json:"id"`
	AccountID string                 `json:"account_id,omitempty"`
	MemoType  string                 `json:"memo_type,omitempty"`
	Memo      string                 `json:"memo,omitempty"`
	Status    string                 `json:"status,omitempty"`
	ExtraInfo map[string]interface{} `json:"extra_info,omitempty"`
}

// InitiateWithdrawal calls an anchor's SEP-6 GET /withdraw to begin a
// non-interactive withdrawal of amount of asset to dest (an off-chain bank
// account identifier the anchor understands).
func (c *Sep6Client) InitiateWithdrawal(ctx context.Context, transferServer, jwt, assetCode, amount, dest string) (*WithdrawalResult, error) {
	q := url.Values{}
	q.Set("asset_code", assetCode)
	q.Set("type", "bank_account")
	if amount != "" {
		q.Set("amount", amount)
	}
	if dest != "" {
		q.Set("dest", dest)
	}

	var out WithdrawalResult
	if err := c.doJSON(ctx, transferServer+"/withdraw?"+q.Encode(), jwt, &out); err != nil {
		return nil, fmt.Errorf("initiate sep-6 withdrawal: %w", err)
	}
	return &out, nil
}

// AnchorTransaction is a SEP-6/24 transaction status object as returned by
// an anchor's GET /transaction.
type AnchorTransaction struct {
	ID                    string `json:"id"`
	Kind                  string `json:"kind"`
	Status                string `json:"status"`
	StatusEta             int    `json:"status_eta,omitempty"`
	AmountIn              string `json:"amount_in,omitempty"`
	AmountOut             string `json:"amount_out,omitempty"`
	AmountFee             string `json:"amount_fee,omitempty"`
	StartedAt             string `json:"started_at,omitempty"`
	CompletedAt           string `json:"completed_at,omitempty"`
	StellarTransactionID  string `json:"stellar_transaction_id,omitempty"`
	ExternalTransactionID string `json:"external_transaction_id,omitempty"`
	Message               string `json:"message,omitempty"`
}

type getTransactionResponse struct {
	Transaction AnchorTransaction `json:"transaction"`
}

// GetTransaction polls an anchor's GET /transaction for the current status
// of a previously initiated deposit or withdrawal.
func (c *Sep6Client) GetTransaction(ctx context.Context, transferServer, jwt, transactionID string) (*AnchorTransaction, error) {
	q := url.Values{}
	q.Set("id", transactionID)

	var out getTransactionResponse
	if err := c.doJSON(ctx, transferServer+"/transaction?"+q.Encode(), jwt, &out); err != nil {
		return nil, fmt.Errorf("get sep-6 transaction: %w", err)
	}
	return &out.Transaction, nil
}

func (c *Sep6Client) doJSON(ctx context.Context, urlStr, jwt string, out interface{}) error {
	return doAnchorRequest(ctx, c.httpClient, http.MethodGet, urlStr, jwt, nil, out)
}

// doAnchorRequest issues an HTTP request to an anchor endpoint (optionally
// bearer-authenticated with a SEP-10 JWT) and decodes a JSON response,
// shared by the SEP-6 and SEP-24 clients.
func doAnchorRequest(ctx context.Context, httpClient *http.Client, method, urlStr, jwt string, body io.Reader, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(respBody, &errBody)
		if errBody.Error != "" {
			return fmt.Errorf("anchor returned status %d: %s", resp.StatusCode, errBody.Error)
		}
		return fmt.Errorf("anchor returned status %d: %s", resp.StatusCode, respBody)
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}
