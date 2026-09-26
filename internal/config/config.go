package config

import (
	"encoding/hex"
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	Port                            string
	Env                             string
	DatabaseURL                     string
	RedisURL                        string
	StellarNetwork                  string
	StellarHorizonURL               string
	StellarCoreURL                  string
	StellarUSDCIssuer               string
	StellarEURCIssuer               string
	MasterEncryptionKey             []byte
	TreasurySecretKey               string
	PlatformFeeWalletPublicKey      string
	ColdStorageAddress              string
	MigrationsPath                  string
	AlertWebhookURL                 string
	PlatformWalletID                string
	FlutterwaveSecretKey            string
	FlutterwaveWebhookHash          string
	BalanceDiscrepancyThreshold     string
	ReconciliationDriftThresholdUSD string
	JWTSecret                       string
	FXSpreadBps                     int
	SorobanRPCURL                   string
	OTELEnabled                     bool
	OTELExporterEndpoint            string
	OTELServiceName                 string
	ContractWalletWasmHash          string
	ContractWalletSpendingLimit     string
	ContractWalletWindowSeconds     int
	ContractWalletRecoveryQuota     int
}

func Load() (*Config, error) {
	viper.AutomaticEnv()

	viper.SetDefault("PORT", "3000")
	viper.SetDefault("ENV", "development")
	viper.SetDefault("STELLAR_NETWORK", "testnet")
	viper.SetDefault("STELLAR_HORIZON_URL", "https://horizon-testnet.stellar.org")
	viper.SetDefault("MIGRATIONS_PATH", "db/migrations")
	viper.SetDefault("RECONCILIATION_DRIFT_THRESHOLD_USD", "1.00")
	viper.SetDefault("OTEL_ENABLED", false)
	viper.SetDefault("OTEL_EXPORTER_ENDPOINT", "http://localhost:4318")
	viper.SetDefault("OTEL_SERVICE_NAME", "fluxa")
	viper.SetDefault("FX_SPREAD_BPS", "50") // default 0.5%
	viper.SetDefault("JWT_SECRET", "fluxa-default-jwt-secret-key-change-in-production")
	viper.SetDefault("SOROBAN_RPC_URL", "https://soroban-testnet.stellar.org")
	viper.SetDefault("CONTRACT_WALLET_SPENDING_LIMIT", "1000")
	viper.SetDefault("CONTRACT_WALLET_WINDOW_SECONDS", "86400")
	viper.SetDefault("CONTRACT_WALLET_RECOVERY_THRESHOLD", "2")

	// Load .env file if present (dev convenience)
	viper.SetConfigFile(".env")
	viper.SetConfigType("env")
	_ = viper.ReadInConfig() // ignore if not exist

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

	env := viper.GetString("ENV")
	jwtSecret := viper.GetString("JWT_SECRET")
	if env == "production" {
		if jwtSecret == "fluxa-default-jwt-secret-key-change-in-production" || len(jwtSecret) < 32 {
			return nil, fmt.Errorf("a secure, high-entropy JWT_SECRET (min 32 bytes) must be explicitly configured in production")
		}
	}

	return &Config{
		Port:                            viper.GetString("PORT"),
		Env:                             env,
		DatabaseURL:                     viper.GetString("DATABASE_URL"),
		RedisURL:                        viper.GetString("REDIS_URL"),
		StellarNetwork:                  viper.GetString("STELLAR_NETWORK"),
		StellarHorizonURL:               viper.GetString("STELLAR_HORIZON_URL"),
		StellarCoreURL:                  viper.GetString("STELLAR_CORE_URL"),
		StellarUSDCIssuer:               viper.GetString("STELLAR_USDC_ISSUER"),
		StellarEURCIssuer:               viper.GetString("STELLAR_EURC_ISSUER"),
		MasterEncryptionKey:             keyBytes,
		TreasurySecretKey:               viper.GetString("TREASURY_SECRET_KEY"),
		PlatformFeeWalletPublicKey:      viper.GetString("PLATFORM_FEE_WALLET_PUBLIC_KEY"),
		ColdStorageAddress:              viper.GetString("COLD_STORAGE_ADDRESS"),
		MigrationsPath:                  viper.GetString("MIGRATIONS_PATH"),
		AlertWebhookURL:                 viper.GetString("ALERT_WEBHOOK_URL"),
		PlatformWalletID:                viper.GetString("PLATFORM_WALLET_ID"),
		FlutterwaveSecretKey:            viper.GetString("FLUTTERWAVE_SECRET_KEY"),
		FlutterwaveWebhookHash:          viper.GetString("FLUTTERWAVE_WEBHOOK_HASH"),
		BalanceDiscrepancyThreshold:     viper.GetString("BALANCE_DISCREPANCY_THRESHOLD"),
		ReconciliationDriftThresholdUSD: viper.GetString("RECONCILIATION_DRIFT_THRESHOLD_USD"),
		JWTSecret:                       viper.GetString("JWT_SECRET"),
		FXSpreadBps:                     viper.GetInt("FX_SPREAD_BPS"),
		SorobanRPCURL:                   viper.GetString("SOROBAN_RPC_URL"),
		OTELEnabled:                     viper.GetBool("OTEL_ENABLED"),
		OTELExporterEndpoint:            viper.GetString("OTEL_EXPORTER_ENDPOINT"),
		OTELServiceName:                 viper.GetString("OTEL_SERVICE_NAME"),
		ContractWalletWasmHash:          viper.GetString("CONTRACT_WALLET_WASM_HASH"),
		ContractWalletSpendingLimit:     viper.GetString("CONTRACT_WALLET_SPENDING_LIMIT"),
		ContractWalletWindowSeconds:     viper.GetInt("CONTRACT_WALLET_WINDOW_SECONDS"),
		ContractWalletRecoveryQuota:     viper.GetInt("CONTRACT_WALLET_RECOVERY_THRESHOLD"),
	}, nil
}
