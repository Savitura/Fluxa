package config

import (
	"encoding/hex"
	"fmt"
	"math"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/viper"
	"github.com/stellar/go/keypair"
)

type Config struct {
	Port                    string
	CORSAllowedOrigins      []string
	Env                     string
	LogLevel                string
	DatabaseURL             string
	ReplicaDatabaseURL      string
	RedisURL                string
	RedisSentinelMasterName string
	RedisSentinelAddrs      []string
	RedisSentinelPassword   string
	StellarNetwork          string
	StellarHorizonURL       string
	StellarHorizonTimeout   time.Duration
	// StellarLive* / StellarTestnet* describe the two isolated environments a
	// tenant can operate in. Live mode talks to mainnet; test mode talks to
	// testnet and funds wallets from Friendbot instead of real funds.
	StellarLiveNetwork              string
	StellarLiveHorizonURL           string
	StellarTestnetNetwork           string
	StellarTestnetHorizonURL        string
	StellarTestnetSorobanRPCURL     string
	FriendbotURL                    string
	StellarUSDCIssuer               string
	StellarEURCIssuer               string
	MasterEncryptionKey             []byte
	TreasurySecretKey               string
	PlatformFeeWalletPublicKey      string
	ColdStorageAddress              string
	MigrationsPath                  string
	AlertWebhookURL                 string
	PlatformWalletID                string
	TreasuryBaseReserve             string
	TreasuryReserveCacheTTLSec      int
	TreasuryReserveConcurrency      int
	FlutterwaveSecretKey            string
	FlutterwaveWebhookHash          string
	BalanceDiscrepancyThreshold     string
	ReconciliationDriftThresholdUSD string
	JWTSecret                       string
	OTELEnabled                     bool
	OTELExporterEndpoint            string
	OTELServiceName                 string
	FXSpreadBps                     int
	SorobanRPCURL                   string
	ContractWalletWasmHash          string
	ContractWalletSpendingLimit     string
	ContractWalletWindowSeconds     int
	ContractWalletRecoveryQuota     int
	YellowCardAPIKey                string
	YellowCardWebhookKey            string
	YellowCardSandbox               bool
	ComplianceEnabled               bool
	OFACSDNURL                      string
	ComplianceStructuringUnit       string
	ComplianceVelocityMax           int
	ComplianceVelocityWindowMin     int
	ComplianceRoundTripMin          int
	ComplianceFuzzyThreshold        int
	ComplianceReloadMinutes         int
	WorkerEnabled                   bool
	WebhookAllowPrivateNetworks     bool
	// ClaimableBalanceSourceWalletID funds claimable balances whose request did
	// not name a source wallet.
	ClaimableBalanceSourceWalletID string

	// IdempotencyTTLHours is the number of hours an idempotency record is
	// retained after creation. The middleware uses this value when computing
	// expires_at. The background cleanup job uses it as a cross-check but
	// relies on the stored expires_at column — so changing this only affects
	// new records, not ones already in the database.
	// Default: 24 hours. Minimum enforced: 1 hour.
	IdempotencyTTLHours int
	// CORSAllowedOriginsConfiguredExplicitly is true when the operator set
	// CORS_ALLOWED_ORIGINS rather than relying on the development default.
	CORSAllowedOriginsConfiguredExplicitly bool

	// Indexer configuration.
	IndexerPaymentsPageLimit int
	IndexerStreamMinBackoff  string
	IndexerStreamMaxBackoff  string
	IndexerSyncPageSize      int
	IndexerStreamConcurrency int
	IndexerStreamMaxWallets  int
	IndexerStreamShardCount  int
	IndexerStreamShardIndex  int
	IndexerMetricsPort       string
	TrustedProxyCIDRs        []netip.Prefix

	// PaymentLinkCheckoutTimeout is how long a payment link may stay in
	// processing with no payment seen before the worker releases it.
	PaymentLinkCheckoutTimeout string

	// Auth rate limiting configuration for /v1/auth/register, /v1/auth/login, /v1/org/invites/accept
	AuthRateLimitIPRPS        float64
	AuthRateLimitIPBurst      int
	AuthRateLimitAccountRPS   float64
	AuthRateLimitAccountBurst int
}

// defaultCORSOrigins is the development-friendly default. Serving it outside
// development is a security smell: it allows any localhost origin to call the
// API. Load warns loudly, but does not fail, so existing deployments keep
// working.
const defaultCORSOrigins = "localhost:*"

// wellKnownTestnetSecrets are secret seeds that are published in Stellar's
// public documentation and SDK samples. The corresponding account is funded by
// friendbot and is public knowledge, so a treasury configured with one of
// these keys would be trivially drainable. Boot rejects them outright.
var wellKnownTestnetSecrets = map[string]struct{}{
	// Stellar developer documentation "testnet root account" seed.
	"SCZANGBA5YHTNYVVV4C3U252E2B6P6F5T3U6MM63WBSBZATAQI3EBTQ4": {},
}

// wellKnownTestnetAddresses are the public keys of accounts whose secrets are
// published above. Rejecting by address too catches any other published seed
// that maps to the same account.
var wellKnownTestnetAddresses = map[string]struct{}{
	"GC2BKLYOOYPDEFJKLKY6FNNRQMGFLVHJKQRGNSSRRGSMPGF32LHCQVGF": {},
	// Stellar testnet root account public key from the developer docs.
	"GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H": {},
}

// wellKnownJWTSecrets are public or trivially guessable strings that have
// appeared in documentation, README examples, or this repository's own source
// history. Any of these must never be used as a signing key in any
// environment.
var wellKnownJWTSecrets = map[string]struct{}{
	// Was the hardcoded viper default in this repo (removed in this commit).
	"fluxa-default-jwt-secret-key-change-in-production": {},
	// Common tutorial / StackOverflow examples.
	"secret":                 {},
	"your-256-bit-secret":    {},
	"your-secret-key":        {},
	"changeme":               {},
	"supersecret":            {},
	"jwt_secret":             {},
	"mysecretkey":            {},
	"my_secret_key":          {},
	"dev_jwt_secret":         {},
	"development_jwt_secret": {},
	"test_jwt_secret":        {},
	"jwt-secret":             {},
	"jwt-secret-key":         {},
	"keyboard cat":           {},
}

func validateStellarAddress(name, value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%s must be set to a valid Stellar address", name)
		}
		return nil
	}
	if _, err := keypair.ParseAddress(value); err != nil {
		return fmt.Errorf("%s is not a valid Stellar address: %w", name, err)
	}
	return nil
}

// Validate checks the load-bearing configuration values. It is called by Load
// so a misconfigured process fails fast at boot instead of silently changing
// money-movement or compliance behaviour at runtime.
func (c *Config) Validate() error {
	// The compliance screener exempts platform-wallet traffic from the
	// behavioural velocity and structuring rules. Without the platform wallet
	// it would instead screen the platform's own refunds, sweeps and fee
	// movements, holding real customer funds. Refuse to run in that state.
	if c.ComplianceEnabled {
		if c.PlatformWalletID == "" {
			return fmt.Errorf("PLATFORM_WALLET_ID is required when COMPLIANCE_ENABLED=true: the velocity screener would otherwise screen the platform's own traffic (refunds, sweeps, fee collection)")
		}
		if _, err := uuid.Parse(c.PlatformWalletID); err != nil {
			return fmt.Errorf("PLATFORM_WALLET_ID is not a valid wallet UUID: %w", err)
		}
	}

	// Treasury operations build on-chain assets, so a treasury key implies the
	// issuers that identify those assets must be present and well-formed.
	treasuryConfigured := c.TreasurySecretKey != ""
	if err := validateStellarAddress("STELLAR_USDC_ISSUER", c.StellarUSDCIssuer, treasuryConfigured); err != nil {
		return err
	}
	if err := validateStellarAddress("STELLAR_EURC_ISSUER", c.StellarEURCIssuer, false); err != nil {
		return err
	}
	if err := validateStellarAddress("PLATFORM_FEE_WALLET_PUBLIC_KEY", c.PlatformFeeWalletPublicKey, false); err != nil {
		return err
	}
	if err := validateStellarAddress("COLD_STORAGE_ADDRESS", c.ColdStorageAddress, false); err != nil {
		return err
	}

	if treasuryConfigured {
		kp, err := keypair.ParseFull(c.TreasurySecretKey)
		if err != nil {
			return fmt.Errorf("TREASURY_SECRET_KEY is not a valid Stellar secret seed: %w", err)
		}
		if _, bad := wellKnownTestnetSecrets[c.TreasurySecretKey]; bad {
			return fmt.Errorf("TREASURY_SECRET_KEY is a well-known testnet key published in Stellar's documentation; use a dedicated, secret-funded key")
		}
		if _, bad := wellKnownTestnetAddresses[kp.Address()]; bad {
			return fmt.Errorf("TREASURY_SECRET_KEY belongs to a well-known publicly funded testnet account (%s); use a dedicated key", kp.Address())
		}
	}

	if !c.CORSAllowedOriginsConfiguredExplicitly && c.Env != "development" && containsLocalhostWildcard(c.CORSAllowedOrigins) {
		fmt.Fprintf(os.Stderr, "WARNING: CORS_ALLOWED_ORIGINS is still the development default (localhost:*); set it explicitly for the %q environment\n", c.Env)
	}

	return nil
}

func containsLocalhostWildcard(origins []string) bool {
	for _, origin := range origins {
		if origin == "localhost:*" ||
			strings.HasPrefix(origin, "http://localhost") ||
			strings.HasPrefix(origin, "https://localhost") {
			return true
		}
	}
	return false
}

func splitCSV(value string) []string {
	parts := []string{}
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

func Load() (*Config, error) {
	viper.AutomaticEnv()

	viper.SetDefault("PORT", "3000")
	viper.SetDefault("CORS_ALLOWED_ORIGINS", defaultCORSOrigins)
	viper.SetDefault("ENV", "development")
	viper.SetDefault("LOG_LEVEL", "info")
	viper.SetDefault("STELLAR_NETWORK", "testnet")
	viper.SetDefault("STELLAR_HORIZON_URL", "https://horizon-testnet.stellar.org")
	viper.SetDefault("STELLAR_HORIZON_TIMEOUT_SECONDS", "10")
	viper.SetDefault("STELLAR_LIVE_NETWORK", "mainnet")
	viper.SetDefault("STELLAR_LIVE_HORIZON_URL", "https://horizon.stellar.org")
	viper.SetDefault("STELLAR_TESTNET_NETWORK", "testnet")
	viper.SetDefault("STELLAR_TESTNET_HORIZON_URL", "https://horizon-testnet.stellar.org")
	viper.SetDefault("STELLAR_TESTNET_SOROBAN_RPC_URL", "https://soroban-testnet.stellar.org")
	viper.SetDefault("FRIENDBOT_URL", "https://friendbot.stellar.org")
	viper.SetDefault("MIGRATIONS_PATH", "db/migrations")
	viper.SetDefault("RECONCILIATION_DRIFT_THRESHOLD_USD", "1.00")
	viper.SetDefault("OTEL_ENABLED", false)
	viper.SetDefault("OTEL_EXPORTER_ENDPOINT", "http://localhost:4318")
	viper.SetDefault("OTEL_SERVICE_NAME", "fluxa")
	viper.SetDefault("FX_SPREAD_BPS", "50")
	// JWT_SECRET has no default — a missing value fails at boot in all environments.
	// Generate with: openssl rand -hex 32
	viper.SetDefault("SOROBAN_RPC_URL", "https://soroban-testnet.stellar.org")
	viper.SetDefault("CONTRACT_WALLET_SPENDING_LIMIT", "1000")
	viper.SetDefault("CONTRACT_WALLET_WINDOW_SECONDS", "86400")
	viper.SetDefault("CONTRACT_WALLET_RECOVERY_THRESHOLD", "2")
	viper.SetDefault("YELLOW_CARD_SANDBOX", "true")
	viper.SetDefault("COMPLIANCE_ENABLED", "true")
	viper.SetDefault("OFAC_SDN_URL", "https://sanctionslistservice.ofac.treas.gov/api/download/sdn.xml")
	viper.SetDefault("COMPLIANCE_STRUCTURING_UNIT", "1000")
	viper.SetDefault("COMPLIANCE_VELOCITY_MAX_TRANSFERS", "10")
	viper.SetDefault("COMPLIANCE_VELOCITY_WINDOW_MINUTES", "10")
	viper.SetDefault("COMPLIANCE_ROUND_TRIP_WINDOW_MINUTES", "60")
	viper.SetDefault("COMPLIANCE_FUZZY_THRESHOLD", "2")
	viper.SetDefault("COMPLIANCE_RELOAD_MINUTES", "15")
	viper.SetDefault("WORKER_ENABLED", "true")
	viper.SetDefault("TREASURY_BASE_RESERVE", "0.5")
	viper.SetDefault("TREASURY_RESERVE_CACHE_TTL_SECONDS", "120")
	viper.SetDefault("TREASURY_RESERVE_CONCURRENCY", "16")
	viper.SetDefault("WEBHOOK_ALLOW_PRIVATE_NETWORKS", "false")
	viper.SetDefault("IDEMPOTENCY_TTL_HOURS", "24")
	viper.SetDefault("INDEXER_PAYMENTS_PAGE_LIMIT", "50")
	viper.SetDefault("INDEXER_STREAM_MIN_BACKOFF", "1s")
	viper.SetDefault("PAYMENT_LINK_CHECKOUT_TIMEOUT", "30m")
	viper.SetDefault("INDEXER_STREAM_MAX_BACKOFF", "30s")
	viper.SetDefault("INDEXER_SYNC_PAGE_SIZE", "100")
	viper.SetDefault("INDEXER_STREAM_CONCURRENCY", 32)
	viper.SetDefault("INDEXER_STREAM_MAX_WALLETS", 32)
	viper.SetDefault("INDEXER_STREAM_SHARD_COUNT", 1)
	viper.SetDefault("INDEXER_STREAM_SHARD_INDEX", 0)
	viper.SetDefault("INDEXER_METRICS_PORT", "9090")
	viper.SetDefault("AUTH_RATE_LIMIT_IP_RPS", "5")
	viper.SetDefault("AUTH_RATE_LIMIT_IP_BURST", "10")
	viper.SetDefault("AUTH_RATE_LIMIT_ACCOUNT_RPS", "1")
	viper.SetDefault("AUTH_RATE_LIMIT_ACCOUNT_BURST", "5")
	viper.SetDefault("TRUSTED_PROXY_CIDRS", "")

	viper.SetConfigFile(".env")
	viper.SetConfigType("env")
	_ = viper.ReadInConfig()

	required := []string{"DATABASE_URL", "REDIS_URL", "MASTER_ENCRYPTION_KEY"}
	for _, key := range required {
		if viper.GetString(key) == "" {
			return nil, fmt.Errorf("required env var %s is not set", key)
		}
	}

	keyHex := viper.GetString("MASTER_ENCRYPTION_KEY")
	keyBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("MASTER_ENCRYPTION_KEY must be a valid hex string: %w", err)
	}
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("MASTER_ENCRYPTION_KEY must be 32 bytes (64 hex chars), got %d bytes", len(keyBytes))
	}
	if err := validateKeyEntropy(keyBytes); err != nil {
		return nil, fmt.Errorf("MASTER_ENCRYPTION_KEY entropy check failed: %w", err)
	}

	// JWT_SECRET is required in every environment — no default is provided.
	// A missing, too-short, or well-known secret fails at boot so that staging,
	// preview, and developer environments are subject to the same bar as
	// production.
	jwtSecret := viper.GetString("JWT_SECRET")
	if jwtSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is not set; generate one with: openssl rand -hex 32")
	}
	if len(jwtSecret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET is too short (%d bytes); minimum is 32 bytes", len(jwtSecret))
	}
	if _, known := wellKnownJWTSecrets[jwtSecret]; known {
		return nil, fmt.Errorf("JWT_SECRET is a well-known public string; use a randomly generated secret")
	}

	ycSandbox, _ := strconv.ParseBool(viper.GetString("YELLOW_CARD_SANDBOX"))
	complianceEnabled, _ := strconv.ParseBool(viper.GetString("COMPLIANCE_ENABLED"))
	workerEnabled, _ := strconv.ParseBool(viper.GetString("WORKER_ENABLED"))
	webhookAllowPrivateNetworks, _ := strconv.ParseBool(viper.GetString("WEBHOOK_ALLOW_PRIVATE_NETWORKS"))

	env := viper.GetString("ENV")

	authRateLimitIPRPS := viper.GetFloat64("AUTH_RATE_LIMIT_IP_RPS")
	if authRateLimitIPRPS <= 0 {
		authRateLimitIPRPS = 5
	}
	authRateLimitIPBurst := viper.GetInt("AUTH_RATE_LIMIT_IP_BURST")
	if authRateLimitIPBurst <= 0 {
		authRateLimitIPBurst = 10
	}
	authRateLimitAccountRPS := viper.GetFloat64("AUTH_RATE_LIMIT_ACCOUNT_RPS")
	if authRateLimitAccountRPS <= 0 {
		authRateLimitAccountRPS = 1
	}
	authRateLimitAccountBurst := viper.GetInt("AUTH_RATE_LIMIT_ACCOUNT_BURST")
	if authRateLimitAccountBurst <= 0 {
		authRateLimitAccountBurst = 5
	}
	trustedProxyCIDRs, err := parseCIDRs(splitCSV(viper.GetString("TRUSTED_PROXY_CIDRS")))
	if err != nil {
		return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS: %w", err)
	}

	if webhookAllowPrivateNetworks && env != "development" {
		return nil, fmt.Errorf("WEBHOOK_ALLOW_PRIVATE_NETWORKS can only be enabled in development environment")
	}

	cfg := &Config{
		Port:                            viper.GetString("PORT"),
		CORSAllowedOrigins:              splitCSV(viper.GetString("CORS_ALLOWED_ORIGINS")),
		Env:                             env,
		LogLevel:                        viper.GetString("LOG_LEVEL"),
		DatabaseURL:                     viper.GetString("DATABASE_URL"),
		ReplicaDatabaseURL:              viper.GetString("REPLICA_DATABASE_URL"),
		RedisURL:                        viper.GetString("REDIS_URL"),
		RedisSentinelMasterName:         viper.GetString("REDIS_SENTINEL_MASTER_NAME"),
		RedisSentinelAddrs:              splitCSV(viper.GetString("REDIS_SENTINEL_ADDRS")),
		RedisSentinelPassword:           viper.GetString("REDIS_SENTINEL_PASSWORD"),
		StellarNetwork:                  viper.GetString("STELLAR_NETWORK"),
		StellarHorizonURL:               viper.GetString("STELLAR_HORIZON_URL"),
		StellarHorizonTimeout:           time.Duration(viper.GetInt("STELLAR_HORIZON_TIMEOUT_SECONDS")) * time.Second,
		StellarLiveNetwork:              viper.GetString("STELLAR_LIVE_NETWORK"),
		StellarLiveHorizonURL:           viper.GetString("STELLAR_LIVE_HORIZON_URL"),
		StellarTestnetNetwork:           viper.GetString("STELLAR_TESTNET_NETWORK"),
		StellarTestnetHorizonURL:        viper.GetString("STELLAR_TESTNET_HORIZON_URL"),
		StellarTestnetSorobanRPCURL:     viper.GetString("STELLAR_TESTNET_SOROBAN_RPC_URL"),
		FriendbotURL:                    viper.GetString("FRIENDBOT_URL"),
		StellarUSDCIssuer:               viper.GetString("STELLAR_USDC_ISSUER"),
		StellarEURCIssuer:               viper.GetString("STELLAR_EURC_ISSUER"),
		MasterEncryptionKey:             keyBytes,
		TreasurySecretKey:               viper.GetString("TREASURY_SECRET_KEY"),
		PlatformFeeWalletPublicKey:      viper.GetString("PLATFORM_FEE_WALLET_PUBLIC_KEY"),
		ColdStorageAddress:              viper.GetString("COLD_STORAGE_ADDRESS"),
		MigrationsPath:                  viper.GetString("MIGRATIONS_PATH"),
		AlertWebhookURL:                 viper.GetString("ALERT_WEBHOOK_URL"),
		PlatformWalletID:                viper.GetString("PLATFORM_WALLET_ID"),
		TreasuryBaseReserve:             viper.GetString("TREASURY_BASE_RESERVE"),
		TreasuryReserveCacheTTLSec:      viper.GetInt("TREASURY_RESERVE_CACHE_TTL_SECONDS"),
		TreasuryReserveConcurrency:      viper.GetInt("TREASURY_RESERVE_CONCURRENCY"),
		FlutterwaveSecretKey:            viper.GetString("FLUTTERWAVE_SECRET_KEY"),
		FlutterwaveWebhookHash:          viper.GetString("FLUTTERWAVE_WEBHOOK_HASH"),
		BalanceDiscrepancyThreshold:     viper.GetString("BALANCE_DISCREPANCY_THRESHOLD"),
		ReconciliationDriftThresholdUSD: viper.GetString("RECONCILIATION_DRIFT_THRESHOLD_USD"),
		JWTSecret:                       jwtSecret,
		OTELEnabled:                     viper.GetBool("OTEL_ENABLED"),
		OTELExporterEndpoint:            viper.GetString("OTEL_EXPORTER_ENDPOINT"),
		OTELServiceName:                 viper.GetString("OTEL_SERVICE_NAME"),
		FXSpreadBps:                     viper.GetInt("FX_SPREAD_BPS"),
		SorobanRPCURL:                   viper.GetString("SOROBAN_RPC_URL"),
		ContractWalletWasmHash:          viper.GetString("CONTRACT_WALLET_WASM_HASH"),
		ContractWalletSpendingLimit:     viper.GetString("CONTRACT_WALLET_SPENDING_LIMIT"),
		ContractWalletWindowSeconds:     viper.GetInt("CONTRACT_WALLET_WINDOW_SECONDS"),
		ContractWalletRecoveryQuota:     viper.GetInt("CONTRACT_WALLET_RECOVERY_THRESHOLD"),
		YellowCardAPIKey:                viper.GetString("YELLOW_CARD_API_KEY"),
		YellowCardWebhookKey:            viper.GetString("YELLOW_CARD_WEBHOOK_KEY"),
		YellowCardSandbox:               ycSandbox,
		ComplianceEnabled:               complianceEnabled,
		OFACSDNURL:                      viper.GetString("OFAC_SDN_URL"),
		ComplianceStructuringUnit:       viper.GetString("COMPLIANCE_STRUCTURING_UNIT"),
		ComplianceVelocityMax:           viper.GetInt("COMPLIANCE_VELOCITY_MAX_TRANSFERS"),
		ComplianceVelocityWindowMin:     viper.GetInt("COMPLIANCE_VELOCITY_WINDOW_MINUTES"),
		ComplianceRoundTripMin:          viper.GetInt("COMPLIANCE_ROUND_TRIP_WINDOW_MINUTES"),
		ComplianceFuzzyThreshold:        viper.GetInt("COMPLIANCE_FUZZY_THRESHOLD"),
		ComplianceReloadMinutes:         viper.GetInt("COMPLIANCE_RELOAD_MINUTES"),
		WorkerEnabled:                   workerEnabled,
		WebhookAllowPrivateNetworks:     webhookAllowPrivateNetworks,
		IndexerPaymentsPageLimit:        viper.GetInt("INDEXER_PAYMENTS_PAGE_LIMIT"),
		IndexerStreamMinBackoff:         viper.GetString("INDEXER_STREAM_MIN_BACKOFF"),
		PaymentLinkCheckoutTimeout:      viper.GetString("PAYMENT_LINK_CHECKOUT_TIMEOUT"),
		IndexerStreamMaxBackoff:         viper.GetString("INDEXER_STREAM_MAX_BACKOFF"),
		IndexerSyncPageSize:             viper.GetInt("INDEXER_SYNC_PAGE_SIZE"),
		IndexerStreamConcurrency:        viper.GetInt("INDEXER_STREAM_CONCURRENCY"),
		IndexerStreamMaxWallets:         viper.GetInt("INDEXER_STREAM_MAX_WALLETS"),
		IndexerStreamShardCount:         viper.GetInt("INDEXER_STREAM_SHARD_COUNT"),
		IndexerStreamShardIndex:         viper.GetInt("INDEXER_STREAM_SHARD_INDEX"),
		IndexerMetricsPort:              viper.GetString("INDEXER_METRICS_PORT"),
		TrustedProxyCIDRs:               trustedProxyCIDRs,
		ClaimableBalanceSourceWalletID:  viper.GetString("CLAIMABLE_BALANCE_SOURCE_WALLET_ID"),
		IdempotencyTTLHours: func() int {
			h := viper.GetInt("IDEMPOTENCY_TTL_HOURS")
			if h < 1 {
				h = 1
			}
			return h
		}(),
		CORSAllowedOriginsConfiguredExplicitly: os.Getenv("CORS_ALLOWED_ORIGINS") != "",

		AuthRateLimitIPRPS:        authRateLimitIPRPS,
		AuthRateLimitIPBurst:      authRateLimitIPBurst,
		AuthRateLimitAccountRPS:   authRateLimitAccountRPS,
		AuthRateLimitAccountBurst: authRateLimitAccountBurst,
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseCIDRs(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", value, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

// validateKeyEntropy checks that the encryption key has sufficient entropy.
// It uses a simple Shannon entropy estimation to reject obviously weak keys
// (e.g., all zeros, repeated patterns, or low-entropy inputs).
func validateKeyEntropy(key []byte) error {
	if len(key) == 0 {
		return fmt.Errorf("key is empty")
	}

	// Check for all zeros.
	allZero := true
	for _, b := range key {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return fmt.Errorf("key cannot be all zeros")
	}

	// Check for all same byte.
	allSame := true
	first := key[0]
	for _, b := range key {
		if b != first {
			allSame = false
			break
		}
	}
	if allSame {
		return fmt.Errorf("key cannot be all identical bytes")
	}

	// Calculate Shannon entropy (bits per byte).
	// For a 32-byte key, we expect entropy close to 8 bits/byte.
	freq := make(map[byte]int)
	for _, b := range key {
		freq[b]++
	}

	entropy := 0.0
	for _, count := range freq {
		p := float64(count) / float64(len(key))
		entropy -= p * math.Log2(p)
	}

	// Shannon entropy over n samples is bounded by log2(n): a 32-byte key can
	// never exceed 5 bits/byte however random it is, so comparing the raw figure
	// against the 8-bit ceiling would reject every possible key. Compare against
	// the maximum this key length can actually reach instead.
	maxEntropy := math.Log2(float64(len(key)))
	if maxEntropy > 8 {
		maxEntropy = 8
	}
	minimum := 0.9 * maxEntropy
	if entropy < minimum {
		return fmt.Errorf("key entropy too low: %.2f bits/byte (minimum %.2f for a %d-byte key)", entropy, minimum, len(key))
	}

	return nil
}
