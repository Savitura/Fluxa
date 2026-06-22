package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fluxa/fluxa/internal/alerting"
	"github.com/fluxa/fluxa/internal/apikey"
	"github.com/fluxa/fluxa/internal/config"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/fluxa/fluxa/internal/fiat/flutterwave"
	"github.com/fluxa/fluxa/internal/fx"
	"github.com/fluxa/fluxa/internal/indexer"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/server"
	"github.com/fluxa/fluxa/internal/settlement"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "run migrations and exit")
	flag.Parse()

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("load config")
	}

	if cfg.Env == "development" {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	ctx := context.Background()

	if err := postgres.RunMigrations(cfg.DatabaseURL, cfg.MigrationsPath); err != nil {
		log.Fatal().Err(err).Msg("run migrations")
	}
	if *migrateOnly {
		log.Info().Msg("migrations complete")
		return
	}

	db, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("connect to database")
	}
	defer db.Close()

	redisOpt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("parse redis url")
	}
	redisClient := redis.NewClient(redisOpt)
	defer redisClient.Close()

	walletRepo := postgres.NewWalletRepo(db)
	txRepo := postgres.NewTransactionRepo(db)
	convRepo := postgres.NewConversionRepo(db)
	feeRepo := postgres.NewFeeRepo(db)
	apiKeyRepo := postgres.NewAPIKeyRepo(db)
	fiatRepo := postgres.NewFiatRepo(db)
	webhookRepo := postgres.NewWebhookRepo(db)
	reconcileRepo := postgres.NewReconcileRepo(db)
	fxQuoteRepo := postgres.NewFXQuoteRepo(db)

	stellarClient := stellar.NewClient(cfg.StellarHorizonURL, cfg.StellarNetwork)
	signer := stellar.NewEnvSigner(cfg.MasterEncryptionKey, cfg.StellarNetwork)

	queueClient := queue.NewClient(cfg.RedisURL)
	defer queueClient.Close()

	feeSvc := fees.NewService(feeRepo)
	walletSvc := wallet.NewService(walletRepo, stellarClient, cfg.MasterEncryptionKey)
	transferSvc := transfer.NewService(txRepo, walletRepo, feeSvc, queueClient)
	webhookSvc := webhook.NewService(webhookRepo, queueClient)

	issuers := map[string]string{
		"USDC": cfg.StellarUSDCIssuer,
		"EURC": cfg.StellarEURCIssuer,
	}
	horizonProvider := fx.NewHorizonProvider(cfg.StellarHorizonURL, []string{"USDC-EURC", "EURC-USDC"}, issuers)
	fxSvc := fx.NewService(
		walletRepo, convRepo, fxQuoteRepo,
		feeSvc, stellarClient, redisClient,
		cfg.StellarUSDCIssuer, []fx.Provider{horizonProvider}, cfg.FXSpreadBps,
	)

	fwProvider := flutterwave.NewProvider(cfg.FlutterwaveSecretKey, cfg.FlutterwaveWebhookHash)
	fiatSvc := fiat.NewService(fiatRepo, fwProvider, fxSvc, transferSvc, cfg.PlatformWalletID, "flutterwave")

	engine := settlement.NewEngine(
		txRepo, walletRepo, feeSvc, stellarClient, signer,
		cfg.StellarNetwork, cfg.StellarUSDCIssuer, cfg.PlatformFeeWalletPublicKey,
	)
	_ = engine

	idx := indexer.New(walletRepo, txRepo, stellarClient)
	_ = idx

	alertClient := alerting.NewClient(cfg.AlertWebhookURL, "fluxa-api")
	reconcileSvc := reconcile.NewService(
		txRepo,
		reconcileRepo,
		stellarClient,
		alertClient,
		queueClient,
		webhookSvc,
		"fluxa-api",
		decimal.Zero,
	)
	reconcileHandler := reconcile.NewHandler(reconcileSvc)

	walletHandler := wallet.NewHandler(walletSvc)
	transferHandler := transfer.NewHandler(transferSvc)
	fxHandler := fx.NewHandler(fxSvc)
	fiatHandler := fiat.NewHandler(fiatSvc)
	feeHandler := fees.NewHandler(feeSvc)
	apikeyHandler := apikey.NewHandler(apiKeyRepo)
	webhookHandler := webhook.NewHandler(webhookSvc)

	srv := server.New(
		walletHandler, transferHandler, fxHandler, fiatHandler,
		feeHandler, reconcileHandler, apikeyHandler, apiKeyRepo,
		webhookHandler, cfg.Port,
	)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info().Str("port", cfg.Port).Msg("fluxa api starting")
		if err := srv.Start(); err != nil {
			log.Error().Err(err).Msg("server stopped")
		}
	}()

	<-quit
	log.Info().Msg("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("server shutdown error")
	}

	log.Info().Msg("goodbye")
}
