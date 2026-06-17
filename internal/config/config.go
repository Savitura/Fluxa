package config

import (
	"encoding/hex"
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	Port             string
	Env              string
	DatabaseURL      string
	RedisURL         string
	StellarNetwork   string
	StellarHorizonURL string
	StellarUSDCIssuer string
	MasterEncryptionKey []byte
	TreasurySecretKey string
	PlatformFeeWalletPublicKey string
	MigrationsPath   string
	AlertWebhookURL  string
}

func Load() (*Config, error) {
	viper.AutomaticEnv()

	viper.SetDefault("PORT", "3000")
	viper.SetDefault("ENV", "development")
	viper.SetDefault("STELLAR_NETWORK", "testnet")
	viper.SetDefault("STELLAR_HORIZON_URL", "https://horizon-testnet.stellar.org")
	viper.SetDefault("MIGRATIONS_PATH", "db/migrations")

	// Load .env file if present (dev convenience)
	viper.SetConfigFile(".env")
	viper.SetConfigType("env")
	_ = viper.ReadInConfig() // not required to exist

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

	return &Config{
		Port:              viper.GetString("PORT"),
		Env:               viper.GetString("ENV"),
		DatabaseURL:       viper.GetString("DATABASE_URL"),
		RedisURL:          viper.GetString("REDIS_URL"),
		StellarNetwork:    viper.GetString("STELLAR_NETWORK"),
		StellarHorizonURL: viper.GetString("STELLAR_HORIZON_URL"),
		StellarUSDCIssuer: viper.GetString("STELLAR_USDC_ISSUER"),
		MasterEncryptionKey: keyBytes,
		TreasurySecretKey: viper.GetString("TREASURY_SECRET_KEY"),
		PlatformFeeWalletPublicKey: viper.GetString("PLATFORM_FEE_WALLET_PUBLIC_KEY"),
		MigrationsPath:    viper.GetString("MIGRATIONS_PATH"),
		AlertWebhookURL:   viper.GetString("ALERT_WEBHOOK_URL"),
	}, nil
}
