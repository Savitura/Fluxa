package compliance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

const (
	sanctionedAddr = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABLOK"
	cleanAddr      = "GCLEANCLEANCLEANCLEANCLEANCLEANCLEANCLEANCLEANCLEANCLEAN"
)

func sanctionsSetFixture(t *testing.T) *SanctionsSet {
	t.Helper()
	set := NewSanctionsSet()
	set.Replace([]*domain.SanctionsEntity{
		{UID: "10001", Name: "Ayman Zawahiri", Address: sanctionedAddr, AddressType: "Digital Currency Address - XLM"},
		{UID: "10001", Name: "Aiman Zawahri"},
		{UID: "10004", Name: "QUIET SHIPPING LLC"},
	}, time.Now().UTC())
	return set
}

func TestSanctionsScreenerBlocksExactAddressMatch(t *testing.T) {
	s := NewSanctionsScreener(sanctionsSetFixture(t), DefaultFuzzyThreshold)

	got, err := s.Screen(context.Background(), domain.ScreeningRequest{
		ToWalletID:  "w-2",
		ToPublicKey: sanctionedAddr,
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if got.Status != domain.ScreeningBlocked {
		t.Fatalf("status = %q, want %q", got.Status, domain.ScreeningBlocked)
	}
	if got.MatchedEntity != "Ayman Zawahiri" {
		t.Fatalf("matched entity = %q, want %q", got.MatchedEntity, "Ayman Zawahiri")
	}
	if len(got.RulesFired) != 1 || got.RulesFired[0] != "sanctions_address_match" {
		t.Fatalf("rules fired = %v, want [sanctions_address_match]", got.RulesFired)
	}
}

func TestSanctionsScreenerAddressMatchIsCaseInsensitive(t *testing.T) {
	s := NewSanctionsScreener(sanctionsSetFixture(t), DefaultFuzzyThreshold)

	got, err := s.Screen(context.Background(), domain.ScreeningRequest{
		ToPublicKey: "  " + lower(sanctionedAddr) + "  ",
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if got.Status != domain.ScreeningBlocked {
		t.Fatalf("status = %q, want blocked for differently-cased address", got.Status)
	}
}

// A near-miss on a name is evidence, not identity: it must hold for review
// rather than block outright.
func TestSanctionsScreenerFuzzyNameMatchHoldsNotBlocks(t *testing.T) {
	s := NewSanctionsScreener(sanctionsSetFixture(t), DefaultFuzzyThreshold)

	tests := []struct {
		name       string
		federation string
		wantRule   string
	}{
		{"exact name", "Ayman Zawahiri", "sanctions_name_match"},
		{"one edit", "Ayman Zawahirn", "sanctions_name_fuzzy_match"},
		{"two edits", "Ayman Zawahi", "sanctions_name_fuzzy_match"},
		{"alias exact", "Aiman Zawahri", "sanctions_name_match"},
		{"punctuation and case only", "  ayman, zawahiri  ", "sanctions_name_match"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Screen(context.Background(), domain.ScreeningRequest{
				ToPublicKey:  cleanAddr,
				ToFederation: tc.federation,
			})
			if err != nil {
				t.Fatalf("Screen returned error: %v", err)
			}
			if got.Status != domain.ScreeningHold {
				t.Fatalf("status = %q, want %q (a name match must never block)", got.Status, domain.ScreeningHold)
			}
			if len(got.RulesFired) != 1 || got.RulesFired[0] != tc.wantRule {
				t.Fatalf("rules fired = %v, want [%s]", got.RulesFired, tc.wantRule)
			}
		})
	}
}

func TestSanctionsScreenerClearsUnrelatedDestination(t *testing.T) {
	s := NewSanctionsScreener(sanctionsSetFixture(t), DefaultFuzzyThreshold)

	got, err := s.Screen(context.Background(), domain.ScreeningRequest{
		ToPublicKey:  cleanAddr,
		ToFederation: "Completely Different Counterparty",
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if got.Status != domain.ScreeningClear {
		t.Fatalf("status = %q, want %q", got.Status, domain.ScreeningClear)
	}
}

func TestSanctionsScreenerNameBeyondThresholdClears(t *testing.T) {
	s := NewSanctionsScreener(sanctionsSetFixture(t), DefaultFuzzyThreshold)

	got, err := s.Screen(context.Background(), domain.ScreeningRequest{
		ToPublicKey:  cleanAddr,
		ToFederation: "Ayman Zawa",
	})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if got.Status != domain.ScreeningClear {
		t.Fatalf("status = %q, want clear for a name 4 edits away", got.Status)
	}
}

// Fail-closed at the source: an unloaded set must error so CompositeScreener
// turns it into a hold, never a silent pass.
func TestSanctionsScreenerErrorsWhenSetNotLoaded(t *testing.T) {
	s := NewSanctionsScreener(NewSanctionsSet(), DefaultFuzzyThreshold)

	_, err := s.Screen(context.Background(), domain.ScreeningRequest{ToPublicKey: cleanAddr})
	if !errors.Is(err, ErrSanctionsSetNotLoaded) {
		t.Fatalf("err = %v, want ErrSanctionsSetNotLoaded", err)
	}
}

func TestSanctionsScreenerErrorsWhenSetIsNil(t *testing.T) {
	s := NewSanctionsScreener(nil, DefaultFuzzyThreshold)

	if _, err := s.Screen(context.Background(), domain.ScreeningRequest{}); err == nil {
		t.Fatal("expected an error from a nil sanctions set, got nil")
	}
}

func TestSanctionsSetReplaceIsAtomicForReaders(t *testing.T) {
	set := sanctionsSetFixture(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			set.Replace([]*domain.SanctionsEntity{
				{UID: "10001", Name: "Ayman Zawahiri", Address: sanctionedAddr},
			}, time.Now().UTC())
		}
	}()

	// The sanctioned address is present in every version of the list, so a
	// reader must never observe it missing mid-rebuild.
	for i := 0; i < 500; i++ {
		if _, ok := set.MatchAddress(sanctionedAddr); !ok {
			t.Fatal("sanctioned address disappeared during a concurrent rebuild")
		}
	}
	<-done
}

func TestSanctionsSetStats(t *testing.T) {
	set := sanctionsSetFixture(t)
	entities, addresses, names, updatedAt, loaded := set.Stats()

	if !loaded {
		t.Fatal("set should report loaded")
	}
	if entities != 3 {
		t.Fatalf("entity count = %d, want 3", entities)
	}
	if addresses != 1 {
		t.Fatalf("address count = %d, want 1", addresses)
	}
	if names != 3 {
		t.Fatalf("name count = %d, want 3", names)
	}
	if updatedAt.IsZero() {
		t.Fatal("updatedAt should be set")
	}
}

func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + ('a' - 'A')
		}
	}
	return string(out)
}

// Regression: an empty sanctions list must not mark the set as loaded.
//
// A fresh database has no sanctions_entities rows, so the startup load
// returns an empty slice. If that marked the set loaded, SanctionsScreener
// would return clear for every destination and fail-closed would be defeated
// exactly when it matters most — a new deployment before its first SDN
// refresh.
func TestEmptyListDoesNotMarkSetLoaded(t *testing.T) {
	set := NewSanctionsSet()
	set.Replace(nil, time.Now().UTC())

	if set.Loaded() {
		t.Fatal("an empty list marked the sanctions set as loaded")
	}

	_, err := NewSanctionsScreener(set, DefaultFuzzyThreshold).
		Screen(context.Background(), domain.ScreeningRequest{ToPublicKey: cleanAddr})
	if !errors.Is(err, ErrSanctionsSetNotLoaded) {
		t.Fatalf("err = %v, want ErrSanctionsSetNotLoaded so screening fails closed", err)
	}
}

// A failed refresh that yields nothing must not wipe a good set.
func TestEmptyReplaceDoesNotDiscardALoadedSet(t *testing.T) {
	set := sanctionsSetFixture(t)

	set.Replace([]*domain.SanctionsEntity{}, time.Now().UTC())

	if !set.Loaded() {
		t.Fatal("an empty replace unloaded a previously loaded set")
	}
	if _, ok := set.MatchAddress(sanctionedAddr); !ok {
		t.Fatal("an empty replace discarded the existing sanctions entries")
	}
}

// The startup path specifically: loading from a repository backed by an empty
// table must leave the screener failing closed.
func TestLoadFromEmptyRepositoryLeavesSetUnloaded(t *testing.T) {
	set := NewSanctionsSet()
	if err := set.LoadFromRepository(context.Background(), newComplianceMockRepo()); err != nil {
		t.Fatalf("LoadFromRepository: %v", err)
	}
	if set.Loaded() {
		t.Fatal("loading from an empty table marked the set loaded")
	}
}
