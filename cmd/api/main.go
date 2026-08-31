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
	"github.com/fluxa/fluxa/internal/compliance"
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
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/treasury"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
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
	var replica *pgxpool.Pool
	if cfg.ReplicaDatabaseURL != "" {
		replica, err = postgres.New(ctx, cfg.ReplicaDatabaseURL)
		if err != nil {
			log.Warn().Err(err).Msg("connect to read replica; reads will use primary")
		}
		if replica != nil {
			defer replica.Close()
		}
	}
	repoDB := postgres.NewReplicaAwareDB(db, replica)

	redisOpt, err := queue.RedisOptions(cfg.RedisURL, cfg.RedisSentinelMasterName, cfg.RedisSentinelAddrs, cfg.RedisSentinelPassword)
	if err != nil {
		log.Fatal().Err(err).Msg("parse redis url")
	}
	redisClient := redis.NewUniversalClient(redisOpt)
	defer redisClient.Close()

	tenantRepo := postgres.NewTenantRepo(repoDB)
	userRepo := postgres.NewUserRepo(repoDB)
	orgRepo := postgres.NewOrgRepo(repoDB)

	walletRepo := postgres.NewWalletRepo(repoDB)
	txRepo := postgres.NewTransactionRepo(repoDB)

	convRepo := postgres.NewConversionRepo(repoDB)
	feeRepo := postgres.NewFeeRepo(repoDB)
	apiKeyRepo := postgres.NewAPIKeyRepo(repoDB)
	fiatRepo := postgres.NewFiatRepo(repoDB)
	webhookRepo := postgres.NewWebhookRepo(repoDB)
	reconcileRepo := postgres.NewReconcileRepo(repoDB)
	fxQuoteRepo := postgres.NewFXQuoteRepo(repoDB)
	batchRepo := postgres.NewBatchRepo(repoDB)
	scheduleRepo := postgres.NewScheduleRepo(repoDB)
	anchorRepo := postgres.NewAnchorRepo(repoDB)
	treasuryRepo := postgres.NewTreasuryRepo(repoDB)
	idempotencyRepo := postgres.NewIdempotencyRepo(repoDB)
	complianceRepo := postgres.NewComplianceRepo(repoDB).WithPrimary(db)
	idemMW := idempotency.Middleware(idempotencyRepo)

	stellarClient := stellar.NewClient(cfg.StellarHorizonURL, cfg.StellarNetwork)
	signer := stellar.NewEnvSigner(cfg.MasterEncryptionKey, cfg.StellarNetwork)

	asynqOpt, err := queue.AsynqRedisOptions(cfg.RedisURL, cfg.RedisSentinelMasterName, cfg.RedisSentinelAddrs, cfg.RedisSentinelPassword)
	if err != nil {
		log.Fatal().Err(err).Msg("configure asynq redis")
	}
	queueClient := queue.NewClientWithOptions(asynqOpt)
	defer queueClient.Close()

	jwtSecretBytes := []byte(cfg.JWTSecret)

	refreshTokenRepo := postgres.NewRefreshTokenRepo(repoDB)
	authSvc := auth.NewService(userRepo, tenantRepo, orgRepo, refreshTokenRepo, jwtSecretBytes)
	orgSvc := org.NewService(orgRepo, userRepo, tenantRepo, jwtSecretBytes)

	feeSvc := fees.NewService(feeRepo)
	walletSvc := wallet.NewService(walletRepo, stellarClient, cfg.MasterEncryptionKey, tenantRepo).
		WithSigner(signer).
		WithIssuers(cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer)
	transferSvc := transfer.NewService(txRepo, walletRepo, feeSvc, queueClient, tenantRepo).
		WithStellarClient(stellarClient)
	webhookSvc := webhook.NewService(webhookRepo, queueClient, tenantRepo)

	// Compliance screening sits in front of settlement, so it is wired before
	// the services that initiate transfers. When disabled, no screener is
	// attached and transfers keep their pre-compliance behaviour.
	var complianceHandler *compliance.Handler
	if cfg.ComplianceEnabled {
		sanctionsSet := compliance.NewSanctionsSet()

		// Not fatal, unlike anchorRegistry.Load: screening fails closed, so an
		// API that boots before the first SDN refresh holds transfers for
		// review rather than clearing them. Log loudly and carry on.
		if err := sanctionsSet.LoadFromRepository(ctx, complianceRepo); err != nil {
			log.Error().Err(err).Msg("compliance: initial sanctions load failed; transfers will be held until it succeeds")
		}
		sanctionsSet.StartReloader(ctx, complianceRepo,
			time.Duration(cfg.ComplianceReloadMinutes)*time.Minute)

		structuringUnit, err := decimal.NewFromString(cfg.ComplianceStructuringUnit)
		if err != nil {
			log.Fatal().Err(err).Msg("parse COMPLIANCE_STRUCTURING_UNIT")
		}

		screener := compliance.NewCompositeScreener(
			compliance.NewSanctionsScreener(sanctionsSet, cfg.ComplianceFuzzyThreshold),
			compliance.NewVelocityScreener(complianceRepo, compliance.VelocityConfig{
				Window:           time.Duration(cfg.ComplianceVelocityWindowMin) * time.Minute,
				MaxTransfers:     cfg.ComplianceVelocityMax,
				StructuringUnit:  structuringUnit,
				RoundTripWindow:  time.Duration(cfg.ComplianceRoundTripMin) * time.Minute,
				PlatformWalletID: cfg.PlatformWalletID,
			}),
		)

		complianceSvc := compliance.NewService(complianceRepo, screener, sanctionsSet, txRepo, queueClient, webhookSvc)
		complianceHandler = compliance.NewHandler(complianceSvc)
		transferSvc = transferSvc.WithScreener(complianceSvc)
	}

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

	// fiat.Service drives exactly one rail. Flutterwave is the live provider;
	// the Yellow Card provider is implemented (internal/fiat/yellowcard) but
	// not wired, because fiat.NewService takes a single Rail and there is no
	// per-request provider selection yet.
	fwProvider := flutterwave.NewProvider(cfg.FlutterwaveSecretKey, cfg.FlutterwaveWebhookHash)

	fiatSvc := fiat.NewService(fiatRepo, fiat.NewRailAdapter(fwProvider), fxSvc, transferSvc, cfg.PlatformWalletID, "flutterwave")

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
		cfg.StellarNetwork, map[string]string{
			"USDC": cfg.StellarUSDCIssuer,
			"EURC": cfg.StellarEURCIssuer,
		}, cfg.PlatformFeeWalletPublicKey,
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

	asynqSrv := asynq.NewServer(asynqOpt, asynq.Config{
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

	if cfg.WorkerEnabled {
		go func() {
			log.Info().Msg("fluxa api: settlement/indexer asynq consumer starting")
			if err := asynqSrv.Run(asynqMux); err != nil {
				log.Error().Err(err).Msg("fluxa api: asynq consumer stopped")
			}
		}()
	}

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
	)
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

	srv := server.New(
		authHandler, orgHandler, walletHandler, transferHandler, fxHandler, fiatHandler,
		anchorFiatHandler, anchorHandler,
		feeHandler, reconcileHandler, apikeyHandler, apiKeyRepo,
		webhookHandler, batchHandler, scheduleHandler, treasuryHandler, complianceHandler, jwtSecretBytes, cfg.Port,
		map[string]server.DependencyCheck{
			"postgres": db.Ping,
			"replica":  func(ctx context.Context) error { return repoDB.ReplicaAvailable(ctx) },
			"redis":    func(ctx context.Context) error { return redisClient.Ping(ctx).Err() },

			"horizon": server.HorizonDependencyCheck(cfg.StellarHorizonURL),
			"worker": func(ctx context.Context) error {
				if _, err := redisClient.Get(ctx, "fluxa:worker:heartbeat").Result(); err != nil {
					return err
				}
				return nil
			},
		},

		orgRepo,
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
