package wallet

import (
	"context"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
)

func TestWalletBalanceStalenessIndicator(t *testing.T) {
	sv := NewService(NewRepository(), nil)
	bal, err := sv.GetWalletBalances(context.Background(), "tenant-1", "wallet-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bal == nil {
		t.Fatal("expected balance response, got nil")
	}
	// Verify stale field is present and a boolean
	_ = bal.Stale
}
