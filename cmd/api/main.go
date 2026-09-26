package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fluxa/fluxa/internal/alerting"
	"github.com/fluxa/fluxa/internal/anchor"
	"github.com/fluxa/fluxa/internal/apikey"
	"github.com/fluxa/fluxa/internal/assets"
	"github.com/fluxa/fluxa/internal/auth"
	"github.com/fluxa/fluxa/internal/batch"
	"github.com/fluxa/fluxa/internal/config"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/fluxa/fluxa/internal/fiat/flutterwave"
	"github.com/fluxa/fluxa/internal/fx"
	"github.com/fluxa/fluxa/internal/indexer"
	"github.com/fluxa/fluxa/internal/org"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/schedule"
	"github.com/fluxa/fluxa/internal/server"
	"github.com/fluxa/fluxa/internal/server/idempotency"
	"github.com/fluxa/fluxa/internal/settlement"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/treasury"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/hibiken/asynq"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tracingShutdown, err := tracing.Init(ctx, tracing.Config{
		Enabled:          cfg.OTELEnabled,
		ExporterEndpoint: cfg.OTELExporterEndpoint,
		ServiceName:      cfg.OTELServiceName,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("initialize tracing")
	}
	defer func() {
		if err := tracing.ShutdownWithTimeout(tracingShutdown, 5*time.Second); err != nil {
			log.Error().Err(err).Msg("tracing shutdown")
		}
	}()

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

	tenantRepo := postgres.NewTenantRepo(db)
	userRepo := postgres.NewUserRepo(db)
	orgRepo := postgres.NewOrgRepo(db)

	walletRepo := postgres.NewWalletRepo(db)
	txRepo := postgres.NewTransactionRepo(db)
	convRepo := postgres.NewConversionRepo(db)
	feeRepo := postgres.NewFeeRepo(db)
	apiKeyRepo := postgres.NewAPIKeyRepo(db)
	fiatRepo := postgres.NewFiatRepo(db)
	webhookRepo := postgres.NewWebhookRepo(db)
	reconcileRepo := postgres.NewReconcileRepo(db)
	fxQuoteRepo := postgres.NewFXQuoteRepo(db)
	batchRepo := postgres.NewBatchRepo(db)
	scheduleRepo := postgres.NewScheduleRepo(db)
	anchorRepo := postgres.NewAnchorRepo(db)
	treasuryRepo := postgres.NewTreasuryRepo(db)
	idempotencyRepo := postgres.NewIdempotencyRepo(db)
	idemMW := idempotency.Middleware(idempotencyRepo)

	stellarClient := stellar.NewClient(cfg.StellarHorizonURL, cfg.StellarNetwork)
	signer := stellar.NewEnvSigner(cfg.MasterEncryptionKey, cfg.StellarNetwork)

	queueClient := queue.NewClient(cfg.RedisURL)
	defer queueClient.Close()

	jwtSecretBytes := []byte(cfg.JWTSecret)

	authSvc := auth.NewService(userRepo, tenantRepo, orgRepo, jwtSecretBytes)
	orgSvc := org.NewService(orgRepo, userRepo, tenantRepo, jwtSecretBytes)

	feeSvc := fees.NewService(feeRepo)
	walletSvc := wallet.NewService(walletRepo, stellarClient, cfg.MasterEncryptionKey, tenantRepo).
		WithSigner(signer).
		WithIssuers(cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer)
	transferSvc := transfer.NewService(txRepo, walletRepo, feeSvc, queueClient, tenantRepo).
		WithStellarClient(stellarClient)
	webhookSvc := webhook.NewConfigService(webhookRepo, webhookRepo, queueClient, tenantRepo)
	batchSvc := batch.NewService(batchRepo, txRepo, transferSvc)
	scheduleSvc := schedule.NewService(scheduleRepo, walletRepo)

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
	walletSvc.WithFXService(fxSvc)

	fwProvider := flutterwave.NewProvider(cfg.FlutterwaveSecretKey, cfg.FlutterwaveWebhookHash)
	fiatSvc := fiat.NewService(fiatRepo, fwProvider, fxSvc, transferSvc, cfg.PlatformWalletID, "flutterwave")

	anchorRegistry := anchor.NewRegistry(anchorRepo, nil)
	if err := anchorRegistry.Load(ctx); err != nil {
		log.Fatal().Err(err).Msg("load anchor registry")
	}
	anchorFiatSvc := fiat.NewAnchorFiatService(anchorRegistry, anchorRepo, walletRepo, cfg.MasterEncryptionKey, cfg.StellarNetwork)

	treasurySvc := treasury.NewService(
		treasuryRepo, stellarClient, fxSvc, webhookSvc,
		cfg.PlatformFeeWalletPublicKey, cfg.StellarNetwork, cfg.TreasurySecretKey,
		cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer,
	)

	engine := settlement.NewEngine(
		txRepo, walletRepo, feeSvc, stellarClient, signer,
		cfg.StellarNetwork, cfg.StellarUSDCIssuer, cfg.PlatformFeeWalletPublicKey,
	)
	settlementWorker := settlement.NewWorker(engine)

	idx := indexer.New(walletRepo, txRepo, stellarClient)
	indexerWorker := indexer.NewWorker(idx)

	// Live Horizon SSE stream keeps local state in sync in near real time.
	// processPayment is idempotent (guarded by ExistsByTxHash), so running
	// this alongside cmd/worker's own stream is safe, just extra capacity.
	go func() {
		if err := idx.StreamAll(ctx, 1000, 0); err != nil {
			log.Error().Err(err).Msg("indexer: stream all wallets failed")
		}
	}()

	redisOpt2, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("parse redis uri for asynq")
	}
	asynqSrv := asynq.NewServer(redisOpt2, asynq.Config{
		Concurrency: 5,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
	})
	asynqMux := asynq.NewServeMux()
	asynqMux.HandleFunc(queue.TypeProcessTransfer, settlementWorker.HandleProcessTransfer)
	asynqMux.HandleFunc(queue.TypeSyncLedger, indexerWorker.HandleSyncLedger)

	go func() {
		log.Info().Msg("fluxa api: settlement/indexer asynq consumer starting")
		if err := asynqSrv.Run(asynqMux); err != nil {
			log.Error().Err(err).Msg("fluxa api: asynq consumer stopped")
		}
	}()

	alertClient := alerting.NewClient(cfg.AlertWebhookURL, "fluxa-api")
	reconcileSvc := reconcile.NewService(
		txRepo,
		reconcileRepo,
		walletRepo,
		stellarClient,
		alertClient,
		queueClient,
		webhookSvc,
		"fluxa-api",
		decimal.Zero,
		assets.NewRegistry(cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer),
		cfg.PlatformFeeWalletPublicKey,
	).WithDriftThreshold(reconcile.ParseDriftThreshold(cfg.ReconciliationDriftThresholdUSD))
	reconcileHandler := reconcile.NewHandler(reconcileSvc)

	authHandler := auth.NewHandler(authSvc)
	orgHandler := org.NewHandler(orgSvc)
	walletHandler := wallet.NewHandler(walletSvc).WithIdempotency(idemMW)

	// Contract wallets are opt-in: without an installed WASM hash the API keeps
	// serving custodial wallets only and the contract routes stay unregistered.
	if cfg.ContractWalletWasmHash != "" {
		sorobanClient := stellar.NewSorobanClient(cfg.SorobanRPCURL, cfg.StellarNetwork)
		spendingLimit, err := decimal.NewFromString(cfg.ContractWalletSpendingLimit)
		if err != nil {
			log.Fatal().Err(err).Msg("parse CONTRACT_WALLET_SPENDING_LIMIT")
		}
		contractSvc := wallet.NewContractWalletAdapter(
			walletRepo,
			sorobanClient,
			wallet.NewSorobanDeployer(sorobanClient, signer, cfg.ContractWalletWasmHash),
			wallet.NewSACResolver(cfg.StellarNetwork, cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer),
			wallet.ContractWalletParams{
				RecoveryThreshold:     uint32(cfg.ContractWalletRecoveryQuota),
				SpendingLimit:         spendingLimit,
				SpendingWindowSeconds: uint64(cfg.ContractWalletWindowSeconds),
			},
		).WithTenantRepo(tenantRepo)
		contractSvc.WithSigner(signer)
		walletHandler = walletHandler.WithContractService(contractSvc).
			WithGuardianGate(server.RequireRole(domain.RoleOwner, domain.RoleAdmin))
	}
	transferHandler := transfer.NewHandler(transferSvc).WithIdempotency(idemMW)
	fxHandler := fx.NewHandler(fxSvc).WithIdempotency(idemMW)
	fiatHandler := fiat.NewHandler(fiatSvc)
	anchorFiatHandler := fiat.NewAnchorHandler(anchorFiatSvc)
	anchorHandler := anchor.NewHandler(anchorRegistry)
	feeHandler := fees.NewHandler(feeSvc)
	apikeyHandler := apikey.NewHandler(apiKeyRepo)
	webhookHandler := webhook.NewHandler(webhookSvc)
	batchHandler := batch.NewHandler(batchSvc).WithIdempotency(idemMW)
	scheduleHandler := schedule.NewHandler(scheduleSvc)
	treasuryHandler := treasury.NewHandler(treasurySvc).WithMutationGate(server.RequireRole(domain.RoleOwner, domain.RoleAdmin))

	// Stellar Core is always reported, even when STELLAR_CORE_URL is unset: a
	// missing Core URL is itself a degraded dependency, and omitting the key
	// would report a healthy service that cannot settle anything.
	healthChecks := map[string]server.DependencyCheck{
		"database": db.Ping,
		"redis": func(ctx context.Context) error {
			return redisClient.Ping(ctx).Err()
		},
		"stellar":      server.HTTPDependencyCheck(cfg.StellarHorizonURL),
		"stellar_core": server.StellarCoreDependencyCheck(cfg.StellarCoreURL),
	}
	healthMonitor := server.NewHealthMonitor(healthChecks)
	healthMonitor.Start(ctx)

	srv := server.New(
		authHandler, orgHandler, walletHandler, transferHandler, fxHandler, fiatHandler,
		anchorFiatHandler, anchorHandler,
		feeHandler, reconcileHandler, apikeyHandler, apiKeyRepo,
		webhookHandler, batchHandler, scheduleHandler, treasuryHandler, jwtSecretBytes, cfg.Port,
		healthChecks,
		server.WithHealthMonitor(healthMonitor),
		server.WithMiddleware(tracing.HTTPMiddleware),
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

	cancel() // stop the indexer's live payment stream

	asynqSrv.Shutdown()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("server shutdown error")
	}

	log.Info().Msg("goodbye")
}
