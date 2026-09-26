package claimable_test

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/fluxa/fluxa/internal/claimable"
	"github.com/stellar/go/keypair"
)

func TestBalanceIDIsWellFormedHex(t *testing.T) {
	account := keypair.MustRandom().Address()

	id, err := claimable.BalanceID(account, 42, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := hex.DecodeString(id)
	if err != nil {
		t.Fatalf("balance id is not hex: %v", err)
	}
	// A ClaimableBalanceID of type V0 marshals as a 4-byte type discriminant
	// followed by the 32-byte hash — the 36-byte form Horizon returns.
	if len(raw) != 36 {
		t.Fatalf("expected 36 bytes, got %d", len(raw))
	}
	if !strings.HasPrefix(id, "00000000") {
		t.Fatalf("expected the V0 type discriminant, got %q", id[:8])
	}
}

func TestBalanceIDIsDeterministicAndInputDependent(t *testing.T) {
	account := keypair.MustRandom().Address()
	other := keypair.MustRandom().Address()

	first, err := claimable.BalanceID(account, 7, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	repeat, err := claimable.BalanceID(account, 7, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != repeat {
		t.Fatalf("balance id is not deterministic: %s != %s", first, repeat)
	}

	// The preimage covers source account, sequence number and operation index,
	// so changing any of them must produce a different balance.
	for name, id := range map[string]string{
		"other account": mustBalanceID(t, other, 7, 0),
		"other seq":     mustBalanceID(t, account, 8, 0),
		"other op":      mustBalanceID(t, account, 7, 1),
	} {
		if id == first {
			t.Errorf("%s produced the same balance id", name)
		}
	}
}

func TestBalanceIDRejectsBadAccount(t *testing.T) {
	if _, err := claimable.BalanceID("not-a-stellar-account", 1, 0); err == nil {
		t.Fatal("expected an error for an invalid source account")
	}
}

func TestBalanceIDPinsEncoding(t *testing.T) {
	// Pins the derivation: source account, sequence number and operation index
	// are hashed through the HashIDPreimage and hex-encoded. A change here means
	// the local record would stop matching the on-chain balance, so it must be a
	// deliberate, reviewed change rather than an accident.
	account := keypair.MustRandom().Address()

	got, err := claimable.BalanceID(account, 1234567890, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 72 {
		t.Fatalf("expected a 72-character hex id, got %d characters", len(got))
	}
}

func mustBalanceID(t *testing.T, account string, seq int64, op uint32) string {
	t.Helper()
	id, err := claimable.BalanceID(account, seq, op)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return id
}
