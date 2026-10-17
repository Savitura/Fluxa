package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// validAddress is a well-formed, checksummed Stellar address.
const validAddress = "GC2BKLYOOYPDEFJKLKY6FNNRQMGFLVHJKQRGNSSRRGSMPGF32LHCQVGF"

const otherValidAddress = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"

// wellKnownSecret is the Stellar-documented testnet root account seed. It is
// used deliberately in the rejection tests.
const wellKnownSecret = "SCZANGBA5YHTNYVVV4C3U252E2B6P6F5T3U6MM63WBSBZATAQI3EBTQ4"

// base64Secret is a syntactically valid, non-published Stellar secret seed.
const freshSecret = "SCLS3N3ULDEF2RAALWFGEOET2F534XNJUCGHE5T3J3T4PB2OHEDS4MGI"

// setRequiredEnv populates the mandatory variables Load() insists on so each
// test only has to override the value under test.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/fluxa")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	// A high-entropy 32-byte key: the entropy guard rejects repeated-byte keys
	// before any other validation runs, so fixtures must use real-looking
	// material to exercise the checks under test.
	t.Setenv("MASTER_ENCRYPTION_KEY", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	t.Setenv("JWT_SECRET", "test-secret-key-that-is-at-least-32-bytes-long-for-validation")
	t.Setenv("COMPLIANCE_ENABLED", "false")
	t.Setenv("ENV", "development")
}

func loadWith(t *testing.T, env map[string]string) (*Config, error) {
	t.Helper()
	viper.Reset()
	setRequiredEnv(t)
	for k, v := range env {
		t.Setenv(k, v)
	}
	return Load()
}

func TestLoad_ComplianceEnabledRequiresPlatformWallet(t *testing.T) {
	_, err := loadWith(t, map[string]string{"COMPLIANCE_ENABLED": "true"})
	if err == nil {
		t.Fatal("expected an error when COMPLIANCE_ENABLED=true and PLATFORM_WALLET_ID is unset")
	}
	if !strings.Contains(err.Error(), "PLATFORM_WALLET_ID") {
		t.Fatalf("error should name PLATFORM_WALLET_ID, got %v", err)
	}
}

func TestLoad_ComplianceEnabledRejectsMalformedPlatformWallet(t *testing.T) {
	_, err := loadWith(t, map[string]string{
		"COMPLIANCE_ENABLED": "true",
		"PLATFORM_WALLET_ID": "not-a-stellar-address",
	})
	if err == nil {
		t.Fatal("expected an error for a malformed PLATFORM_WALLET_ID")
	}
	if !strings.Contains(err.Error(), "PLATFORM_WALLET_ID") {
		t.Fatalf("error should name PLATFORM_WALLET_ID, got %v", err)
	}
}

func TestLoad_ComplianceEnabledAcceptsValidPlatformWallet(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		"COMPLIANCE_ENABLED": "true",
		"PLATFORM_WALLET_ID": "550e8400-e29b-41d4-a716-446655440000",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PlatformWalletID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("PlatformWalletID = %q, want %q", cfg.PlatformWalletID, "550e8400-e29b-41d4-a716-446655440000")
	}
}

func TestLoad_ComplianceDisabledAllowsMissingPlatformWallet(t *testing.T) {
	if _, err := loadWith(t, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_RejectsMalformedIssuers(t *testing.T) {
	cases := map[string]map[string]string{
		"usdc": {"STELLAR_USDC_ISSUER": "bogus"},
		"eurc": {"STELLAR_EURC_ISSUER": "bogus"},
		"fee":  {"PLATFORM_FEE_WALLET_PUBLIC_KEY": "bogus"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadWith(t, env)
			if err == nil {
				t.Fatal("expected a validation error for a malformed address")
			}
			if !strings.Contains(err.Error(), "Stellar address") {
				t.Fatalf("error should mention a Stellar address, got %v", err)
			}
		})
	}
}

func TestLoad_TreasuryRequiresUSDCIssuer(t *testing.T) {
	_, err := loadWith(t, map[string]string{"TREASURY_SECRET_KEY": freshSecret})
	if err == nil {
		t.Fatal("expected an error when a treasury key is set without STELLAR_USDC_ISSUER")
	}
	if !strings.Contains(err.Error(), "STELLAR_USDC_ISSUER") {
		t.Fatalf("error should name STELLAR_USDC_ISSUER, got %v", err)
	}
}

func TestLoad_RejectsMalformedTreasurySecret(t *testing.T) {
	_, err := loadWith(t, map[string]string{
		"TREASURY_SECRET_KEY": "not-a-secret",
		"STELLAR_USDC_ISSUER": validAddress,
	})
	if err == nil {
		t.Fatal("expected an error for a malformed TREASURY_SECRET_KEY")
	}
	if !strings.Contains(err.Error(), "TREASURY_SECRET_KEY") {
		t.Fatalf("error should name TREASURY_SECRET_KEY, got %v", err)
	}
}

func TestLoad_RejectsWellKnownTestnetTreasurySecret(t *testing.T) {
	_, err := loadWith(t, map[string]string{
		"TREASURY_SECRET_KEY": wellKnownSecret,
		"STELLAR_USDC_ISSUER": validAddress,
	})
	if err == nil {
		t.Fatal("expected a well-known testnet key to be rejected")
	}
	if !strings.Contains(err.Error(), "well-known testnet") {
		t.Fatalf("error should explain the key is well-known, got %v", err)
	}
}

func TestLoad_AcceptsWellFormedTreasuryConfig(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		"TREASURY_SECRET_KEY": freshSecret,
		"STELLAR_USDC_ISSUER": validAddress,
		"STELLAR_EURC_ISSUER": otherValidAddress,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TreasurySecretKey != freshSecret {
		t.Fatalf("TreasurySecretKey was not loaded")
	}
}

func TestValidateStellarAddress(t *testing.T) {
	if err := validateStellarAddress("X", "", true); err == nil {
		t.Fatal("required empty value should fail")
	}
	if err := validateStellarAddress("X", validAddress, true); err != nil {
		t.Fatalf("valid address should pass: %v", err)
	}
	if err := validateStellarAddress("X", "nope", false); err == nil {
		t.Fatal("malformed optional value should fail")
	}
	if err := validateStellarAddress("X", "", false); err != nil {
		t.Fatalf("optional empty value should pass: %v", err)
	}
}

func TestLoad_AuthRateLimitDefaultsAndOverrides(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg, err := loadWith(t, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AuthRateLimitIPRPS != 5 {
			t.Errorf("AuthRateLimitIPRPS = %v, want 5", cfg.AuthRateLimitIPRPS)
		}
		if cfg.AuthRateLimitIPBurst != 10 {
			t.Errorf("AuthRateLimitIPBurst = %v, want 10", cfg.AuthRateLimitIPBurst)
		}
		if cfg.AuthRateLimitAccountRPS != 1 {
			t.Errorf("AuthRateLimitAccountRPS = %v, want 1", cfg.AuthRateLimitAccountRPS)
		}
		if cfg.AuthRateLimitAccountBurst != 5 {
			t.Errorf("AuthRateLimitAccountBurst = %v, want 5", cfg.AuthRateLimitAccountBurst)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		cfg, err := loadWith(t, map[string]string{
			"AUTH_RATE_LIMIT_IP_RPS":        "20",
			"AUTH_RATE_LIMIT_IP_BURST":      "50",
			"AUTH_RATE_LIMIT_ACCOUNT_RPS":   "5",
			"AUTH_RATE_LIMIT_ACCOUNT_BURST": "15",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AuthRateLimitIPRPS != 20 {
			t.Errorf("AuthRateLimitIPRPS = %v, want 20", cfg.AuthRateLimitIPRPS)
		}
		if cfg.AuthRateLimitIPBurst != 50 {
			t.Errorf("AuthRateLimitIPBurst = %v, want 50", cfg.AuthRateLimitIPBurst)
		}
		if cfg.AuthRateLimitAccountRPS != 5 {
			t.Errorf("AuthRateLimitAccountRPS = %v, want 5", cfg.AuthRateLimitAccountRPS)
		}
		if cfg.AuthRateLimitAccountBurst != 15 {
			t.Errorf("AuthRateLimitAccountBurst = %v, want 15", cfg.AuthRateLimitAccountBurst)
		}
	})
}

func TestLoad_TrustedProxyCIDRs(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{"TRUSTED_PROXY_CIDRS": "10.0.0.0/8,2001:db8::/32"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 || cfg.TrustedProxyCIDRs[0].String() != "10.0.0.0/8" || cfg.TrustedProxyCIDRs[1].String() != "2001:db8::/32" {
		t.Fatalf("TrustedProxyCIDRs = %v, want [10.0.0.0/8 2001:db8::/32]", cfg.TrustedProxyCIDRs)
	}

	if _, err := loadWith(t, map[string]string{"TRUSTED_PROXY_CIDRS": "not-a-cidr"}); err == nil {
		t.Fatal("invalid trusted proxy CIDR should fail configuration loading")
	}
}
