package fees

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Stellar network fee constants. A stroop is 1e-7 of the native asset, and the
// protocol's minimum base fee is 100 stroops per operation.
const (
	// DefaultBaseFeeStroops is the protocol minimum, used whenever a live fee
	// source is unavailable so an estimate is always deterministic.
	DefaultBaseFeeStroops int64 = 100
	// StroopsPerUnit is the number of stroops in one unit of the native asset.
	StroopsPerUnit int64 = 10_000_000
	// DefaultMaxOperationsPerTransaction is the Stellar protocol cap on
	// operations in a single transaction.
	DefaultMaxOperationsPerTransaction = 100
	// MaxEstimateOperations bounds how many operations a single preflight
	// request may estimate, keeping the response and the multiplication
	// bounded.
	MaxEstimateOperations = 10_000
)

// NetworkFeeSource reports the current Stellar base fee per operation, in
// stroops. Implementations must never fail an estimate: a source that cannot
// reach the network returns its configured fallback instead of an error, so
// preflight stays deterministic.
type NetworkFeeSource interface {
	BaseFeeStroops(ctx context.Context) (int64, error)
}

// StaticNetworkFeeSource is a fixed fee source. It is the default, used in
// tests and whenever no live source is configured.
type StaticNetworkFeeSource struct {
	BaseFee int64
}

func (s StaticNetworkFeeSource) BaseFeeStroops(context.Context) (int64, error) {
	if s.BaseFee <= 0 {
		return DefaultBaseFeeStroops, nil
	}
	return s.BaseFee, nil
}

// HorizonNetworkFeeSource reads the base fee from Horizon's /fee_stats. It uses
// the recent maximum base fee so the estimate accounts for surge pricing; the
// protocol minimum is a floor.
type HorizonNetworkFeeSource struct {
	HorizonURL string
	Client     *http.Client
	// Fallback is returned when Horizon is unreachable. A non-positive value
	// falls back to DefaultBaseFeeStroops.
	Fallback int64
}

// NewHorizonNetworkFeeSource builds a source for the given Horizon endpoint.
func NewHorizonNetworkFeeSource(horizonURL string, fallbackStroops int64) *HorizonNetworkFeeSource {
	return &HorizonNetworkFeeSource{
		HorizonURL: horizonURL,
		Client:     &http.Client{Timeout: 3 * time.Second},
		Fallback:   fallbackStroops,
	}
}

func (h *HorizonNetworkFeeSource) fallback() int64 {
	if h.Fallback <= 0 {
		return DefaultBaseFeeStroops
	}
	return h.Fallback
}

type horizonFeeStats struct {
	LastLedgerBaseFee string `json:"last_ledger_base_fee"`
	MaxFee            struct {
		LastLedgerBaseFee string `json:"last_ledger_base_fee"`
	} `json:"max_fee"`
}

func (h *HorizonNetworkFeeSource) BaseFeeStroops(ctx context.Context) (int64, error) {
	if strings.TrimSpace(h.HorizonURL) == "" {
		return h.fallback(), nil
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	endpoint := strings.TrimRight(h.HorizonURL, "/") + "/fee_stats"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return h.fallback(), nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return h.fallback(), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return h.fallback(), nil
	}
	var stats horizonFeeStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return h.fallback(), nil
	}
	base := parseStroops(stats.MaxFee.LastLedgerBaseFee)
	if base <= 0 {
		base = parseStroops(stats.LastLedgerBaseFee)
	}
	if base < DefaultBaseFeeStroops {
		base = DefaultBaseFeeStroops
	}
	return base, nil
}

func parseStroops(raw string) int64 {
	var value int64
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &value); err != nil {
		return 0
	}
	return value
}
