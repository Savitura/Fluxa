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
	"github.com/fluxa/fluxa/internal/audit"
	"github.com/fluxa/fluxa/internal/auth"
	"github.com/fluxa/fluxa/internal/batch"
	"github.com/fluxa/fluxa/internal/beneficiary"
	"github.com/fluxa/fluxa/internal/claimable"
	"github.com/fluxa/fluxa/internal/compliance"
	"github.com/fluxa/fluxa/internal/config"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/fluxa/fluxa/internal/fiat/flutterwave"
	"github.com/fluxa/fluxa/internal/fx"
	fluxahealth "github.com/fluxa/fluxa/internal/health"
	"github.com/fluxa/fluxa/internal/indexer"
	"github.com/fluxa/fluxa/internal/logging"
	"github.com/fluxa/fluxa/internal/org"
	"github.com/fluxa/fluxa/internal/paymentlink"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/refund"
	"github.com/fluxa/fluxa/internal/schedule"
	"github.com/fluxa/fluxa/internal/server"
	"github.com/fluxa/fluxa/internal/server/idempotency"
	"github.com/fluxa/fluxa/internal/settlement"
	"github.com/fluxa/fluxa/internal/status"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenantdata"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/transferapproval"
	"github.com/fluxa/fluxa/internal/treasury"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/fluxa/fluxa/internal/wallet_balance_alert"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

func main() {
	migrateOnly := flag.Bool("migrate-only", false, "run migrations and exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("load config")
	}

	logger, err := logging.New(os.Stdout, cfg.LogLevel)
	if err != nil {
		log.Fatal().Err(err).Msg("configure logger")
	}
	log.Logger = logger

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
	txRepo := postgres.NewTransactionRepo(repoDB).WithPrimary(db)

	convRepo := postgres.NewConversionRepo(repoDB)
	feeRepo := postgres.NewFeeRepo(repoDB)
	apiKeyRepo := postgres.NewAPIKeyRepo(repoDB)
	fiatRepo := postgres.NewFiatRepo(repoDB)
	webhookRepo := postgres.NewWebhookRepository(repoDB)
	reconcileRepo := postgres.NewReconcileRepo(repoDB)
	fxQuoteRepo := postgres.NewFXQuoteRepo(repoDB)
	batchRepo := postgres.NewBatchRepo(repoDB)
	scheduleRepo := postgres.NewScheduleRepo(repoDB)
	anchorRepo := postgres.NewAnchorRepo(repoDB)
	treasuryRepo := postgres.NewTreasuryRepo(repoDB)
	incidentRepo := postgres.NewIncidentRepository(db)
	idempotencyRepo := postgres.NewIdempotencyRepo(repoDB)
	complianceRepo := postgres.NewComplianceRepo(repoDB).WithPrimary(db)
	idemMW := idempotency.MiddlewareWithOptions(idempotencyRepo, idempotency.Options{
		TTL: time.Duration(cfg.IdempotencyTTLHours) * time.Hour,
	})
	// Transfers reconcile their durable transaction before creating or
	// enqueueing anything, so they may safely take over a lease left behind by
	// a crashed process. Other endpoints keep the conservative behaviour.
	transferIdemMW := idempotency.MiddlewareWithOptions(idempotencyRepo, idempotency.Options{
		TTL:                time.Duration(cfg.IdempotencyTTLHours) * time.Hour,
		AllowLeaseRecovery: true,
	})
	batchIdemMW := idempotency.MiddlewareWithOptions(idempotencyRepo, idempotency.Options{
		Required: true,
		TTL:      time.Duration(cfg.IdempotencyTTLHours) * time.Hour,
	})
	scheduleIdemMW := idempotency.MiddlewareWithOptions(idempotencyRepo, idempotency.Options{
		Required: true,
		TTL:      time.Duration(cfg.IdempotencyTTLHours) * time.Hour,
	})

	// Live and test environments are separate Horizon clients, signers, and
	// networks; the resolver picks one from the authenticated key's mode.
	stellarClient := stellar.NewClient(cfg.StellarLiveHorizonURL, cfg.StellarLiveNetwork, cfg.StellarHorizonTimeout)
	testStellarClient := stellar.NewClient(cfg.StellarTestnetHorizonURL, cfg.StellarTestnetNetwork, cfg.StellarHorizonTimeout)
	clientResolver := stellar.NewModeAwareClients(stellarClient, testStellarClient)
	signer := stellar.NewEnvSigner(cfg.MasterEncryptionKey, cfg.StellarLiveNetwork)
	testSigner := stellar.NewEnvSigner(cfg.MasterEncryptionKey, cfg.StellarTestnetNetwork)
	signerResolver := stellar.NewModeAwareSigners(signer, testSigner)

	asynqOpt, err := queue.AsynqRedisOptions(cfg.RedisURL, cfg.RedisSentinelMasterName, cfg.RedisSentinelAddrs, cfg.RedisSentinelPassword)
	if err != nil {
		log.Fatal().Err(err).Msg("configure asynq redis")
	}
	queueClient := queue.NewClientWithOptions(asynqOpt)
	defer queueClient.Close()

	jwtSecretBytes := []byte(cfg.JWTSecret)

	refreshTokenRepo := postgres.NewRefreshTokenRepo(repoDB)
	authSvc := auth.NewService(repoDB, userRepo, tenantRepo, orgRepo, apiKeyRepo, webhookRepo, refreshTokenRepo, jwtSecretBytes)
	orgSvc := org.NewService(repoDB, orgRepo, userRepo, tenantRepo, jwtSecretBytes)

	feeSvc := fees.NewEstimatorService(feeRepo, fees.NewHorizonNetworkFeeSource(cfg.StellarHorizonURL, fees.DefaultBaseFeeStroops))
	walletSvc := wallet.NewServiceWithNetwork(walletRepo, stellarClient, cfg.MasterEncryptionKey, cfg.StellarLiveNetwork, tenantRepo).
		WithSigner(signer).
		WithClientResolver(clientResolver).
		WithSignerResolver(signerResolver).
		WithTestnetProvisioner(wallet.NewFriendbotProvisioner(cfg.FriendbotURL)).
		WithIssuers(cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer)
	transferSvc := transfer.ConfigureClientResolver(
		transfer.ConfigureStellarClient(transfer.NewService(txRepo, walletRepo, feeSvc, queueClient, tenantRepo), stellarClient),
		clientResolver,
	)
	webhookSvc := webhook.NewService(webhookRepo, redisClient, queueClient, 120, cfg.WebhookAllowPrivateNetworks, cfg.MasterEncryptionKey)
	if configSvc, ok := webhookSvc.(webhook.ConfigService); ok {
		if err := configSvc.MigrateLegacySigningSecrets(ctx); err != nil {
			log.Fatal().Err(err).Msg("migrate tenant webhook signing secrets")
		}
	}

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

		velocityScreener := compliance.NewVelocityScreener(complianceRepo, compliance.VelocityConfig{
			Window:           time.Duration(cfg.ComplianceVelocityWindowMin) * time.Minute,
			MaxTransfers:     cfg.ComplianceVelocityMax,
			StructuringUnit:  structuringUnit,
			RoundTripWindow:  time.Duration(cfg.ComplianceRoundTripMin) * time.Minute,
			PlatformWalletID: cfg.PlatformWalletID,
		})
		if err := velocityScreener.Validate(); err != nil {
			log.Fatal().Err(err).Msg("velocity screener misconfigured")
		}

		screener := compliance.NewCompositeScreener(
			compliance.NewSanctionsScreener(sanctionsSet, cfg.ComplianceFuzzyThreshold),
			velocityScreener,
		)

		complianceSvc := compliance.NewService(complianceRepo, screener, sanctionsSet, txRepo, queueClient, webhookSvc)
		complianceHandler = compliance.NewHandler(complianceSvc)
		transferSvc = transfer.ConfigureScreener(transferSvc, complianceSvc)
	}

	batchSvc := batch.NewService(batchRepo, txRepo, transferSvc)
	scheduleSvc := schedule.NewService(scheduleRepo, walletRepo)

	issuers := map[string]string{
		"USDC": cfg.StellarUSDCIssuer,
		"EURC": cfg.StellarEURCIssuer,
		"XLM":  "", // native asset — no issuer
	}
	fxPairs := append([]string{"USDC-EURC", "EURC-USDC"}, fx.DefaultXLMFXPairs()...)
	horizonProvider := fx.NewHorizonProvider(cfg.StellarHorizonURL, fxPairs, issuers)
	// CoinGecko oracle backs XLM pairs when DEX order-book liquidity is thin.
	oracleProvider := fx.NewCoinGeckoProvider(fx.DefaultXLMFXPairs())
	fxSvc := fx.NewService(
		walletRepo, convRepo, fxQuoteRepo,
		feeSvc, stellarClient, redisClient,
		cfg.StellarUSDCIssuer, []fx.Provider{horizonProvider, oracleProvider}, cfg.FXSpreadBps,
	)
	walletSvc.WithFXService(fxSvc)

	// fiat.Service drives exactly one rail. Flutterwave is the live provider;
	// the Yellow Card provider is implemented (internal/fiat/yellowcard) but
	// not wired, because fiat.NewService takes a single Rail and there is no
	// per-request provider selection yet.
	fwProvider := flutterwave.NewProvider(cfg.FlutterwaveSecretKey, cfg.FlutterwaveWebhookHash)

	fiatSvc := fiat.NewService(fiatRepo, fiat.NewRailAdapter(fwProvider), fxSvc, transferSvc, cfg.PlatformWalletID, "flutterwave", fiatRepo)
	refundTransferSvc, ok := transferSvc.(refund.TransferService)
	if !ok {
		log.Fatal().Msg("transfer service does not support extended transfers")
	}
	refundSvc := refund.NewService(postgres.NewRefundRepo(repoDB), refundTransferSvc)

	anchorRegistry := anchor.NewRegistry(anchorRepo, nil)
	if err := anchorRegistry.Load(ctx); err != nil {
		log.Fatal().Err(err).Msg("load anchor registry")
	}
	anchorFiatSvc := fiat.NewAnchorFiatService(anchorRegistry, anchorRepo, walletRepo, cfg.MasterEncryptionKey, cfg.StellarNetwork)

	treasurySvc := treasury.NewService(
		treasuryRepo, stellarClient, fxSvc, webhookSvc,
		cfg.PlatformFeeWalletPublicKey, cfg.StellarNetwork, cfg.TreasurySecretKey,
		cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer,
		treasury.OptionsFromConfig(cfg.TreasuryBaseReserve, cfg.TreasuryReserveCacheTTLSec, cfg.TreasuryReserveConcurrency)...,
	)

	engine := settlement.NewEngine(
		txRepo, walletRepo, feeSvc, stellarClient, signer,
		cfg.StellarLiveNetwork, map[string]string{
			"USDC": cfg.StellarUSDCIssuer,
			"EURC": cfg.StellarEURCIssuer,
		}, cfg.PlatformFeeWalletPublicKey,
	).WithClientResolver(clientResolver).WithSignerResolver(signerResolver)
	settlementWorker := settlement.NewWorker(engine)

	idx := indexer.New(walletRepo, txRepo, stellarClient)
	indexerWorker := indexer.NewWorker(idx, cfg)

	asynqSrv := asynq.NewServer(asynqOpt, asynq.Config{
		Concurrency: 5,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
	})
	asynqMux := asynq.NewServeMux()
	asynqMux.Use(logging.WorkerMiddleware(log.Logger))
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
	auditRepo := postgres.NewAuditRepo(repoDB)
	auditSvc := audit.NewService(auditRepo)
	reconcileHandler = reconcileHandler.WithAuditLogger(auditSvc)
	auditHandler := audit.NewHandler(auditSvc)
	usageHandler := server.NewUsageHandler(repoDB)
	beneficiarySvc := beneficiary.NewService(postgres.NewBeneficiaryRepo(repoDB), auditSvc)
	transferSvc = transfer.ConfigureBeneficiaryChecker(transferSvc, beneficiarySvc)
	walletBalanceAlertSvc := wallet_balance_alert.NewService(postgres.NewWalletBalanceAlertRepo(repoDB), auditSvc)

	transferHandler := transfer.NewHandler(transferSvc).WithIdempotency(transferIdemMW)
	transferApprovalSvc := transferapproval.NewService(postgres.NewTransferApprovalRepo(repoDB), queueClient)
	transferApprovalHandler := transferapproval.NewHandler(transferApprovalSvc)
	fxHandler := fx.NewHandler(fxSvc).WithIdempotency(idemMW)
	fiatHandler := fiat.NewHandler(fiatSvc).WithIdempotency(idemMW)
	paymentLinkHandler := paymentlink.NewHandler(paymentlink.NewService(postgres.NewPaymentLinkRepo(repoDB), fiatSvc)).WithIdempotency(idemMW).WithAuditLogger(auditSvc)
	refundHandler := refund.NewHandler(refundSvc).WithIdempotency(idemMW).WithAuditLogger(auditSvc)
	anchorFiatHandler := fiat.NewAnchorHandler(anchorFiatSvc)
	anchorHandler := anchor.NewHandler(anchorRegistry)
	feeHandler := fees.NewHandler(feeSvc)
	apikeyHandler := apikey.NewHandler(apiKeyRepo).WithAuditLogger(auditSvc)
	webhookHandler := webhook.NewHandler(webhookSvc).
		WithIdempotency(idempotency.MiddlewareWithOptions(idempotencyRepo, idempotency.Options{
			Required:              true,
			ResponseEncryptionKey: cfg.MasterEncryptionKey,
		})).
		WithAuditLogger(auditSvc)
	assetRegistry := assets.NewRegistry(cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer)
	batchHandler := batch.NewHandler(batchSvc).WithIdempotency(batchIdemMW).WithAssetValidator(assetRegistry.IsSupported)
	scheduleHandler := schedule.NewHandler(scheduleSvc).
		WithIdempotency(scheduleIdemMW).
		WithAuditLogger(auditSvc)
	treasuryHandler := treasury.NewHandler(treasurySvc).WithMutationGate(server.RequireRole(domain.RoleOwner, domain.RoleAdmin))
	healthChecks := map[string]server.DependencyCheck{
		"postgres": db.Ping,
		"replica":  func(ctx context.Context) error { return repoDB.ReplicaAvailable(ctx) },
		"redis":    func(ctx context.Context) error { return redisClient.Ping(ctx).Err() },
		"horizon":  server.HorizonDependencyCheck(cfg.StellarHorizonURL),
		"worker": func(ctx context.Context) error {
			_, err := redisClient.Get(ctx, "fluxa:worker:heartbeat").Result()
			return err
		},
	}
	healthHistoryRepo := postgres.NewDependencyHealthRepository(repoDB)
	dependencyNames := []string{"postgres", "replica", "redis", "horizon", "worker"}
	statusSvc := status.NewService(incidentRepo).WithDependencyHistory(healthHistoryRepo, dependencyNames)
	statusHandler := status.NewHandler(statusSvc)
	healthSamplerChecks := make(map[string]fluxahealth.DependencyCheck, len(healthChecks))
	for name, check := range healthChecks {
		healthSamplerChecks[name] = fluxahealth.DependencyCheck(check)
	}
	fluxahealth.NewSampler(healthSamplerChecks, healthHistoryRepo).Start(ctx)
	beneficiaryHandler := beneficiary.NewHandler(beneficiarySvc)
	walletBalanceAlertHandler := wallet_balance_alert.NewHandler(walletBalanceAlertSvc)
	tenantDataHandler := tenantdata.NewHandler(tenantdata.NewService(repoDB))

	// Claimable balances move real funds in both directions, so the mutating
	// routes share the Owner/Admin gate used by /v1/keys and the treasury.
	claimableSvc := claimable.NewService(
		postgres.NewClaimableBalanceRepo(repoDB),
		stellarClient,
		stellar.NewClaimableBalanceClientWithTimeout(cfg.StellarHorizonURL, cfg.StellarHorizonTimeout),
		signer,
		postgres.NewClaimableWalletResolver(walletRepo),
		webhookSvc,
		cfg.ClaimableBalanceSourceWalletID,
		map[string]string{
			"USDC": cfg.StellarUSDCIssuer,
			"EURC": cfg.StellarEURCIssuer,
		},
	)
	claimableHandler := claimable.NewHandler(claimableSvc).
		WithIdempotency(idemMW).
		WithMutationGate(server.RequireRole(domain.RoleOwner, domain.RoleAdmin))
	idempotencyHandler := idempotency.NewHandler(idempotencyRepo)

	srv := server.New(
		authHandler, orgHandler, walletHandler, transferHandler, fxHandler, fiatHandler,
		anchorFiatHandler, anchorHandler,
		feeHandler, reconcileHandler, apikeyHandler, apiKeyRepo,
		webhookHandler, batchHandler, scheduleHandler, treasuryHandler, claimableHandler,
		statusHandler, complianceHandler, auditHandler, usageHandler, jwtSecretBytes, cfg.Port,
		healthChecks,

		orgRepo,
		cfg.CORSAllowedOrigins,
		server.AuthRateLimitConfig{
			IPRPS:        cfg.AuthRateLimitIPRPS,
			IPBurst:      cfg.AuthRateLimitIPBurst,
			AccountRPS:   cfg.AuthRateLimitAccountRPS,
			AccountBurst: cfg.AuthRateLimitAccountBurst,
		},
		beneficiaryHandler,
		walletBalanceAlertHandler,
		paymentLinkHandler,
		refundHandler,
		idempotencyHandler,
		transferApprovalHandler,
		tenantDataHandler,
		server.AuditScopeDenials(auditSvc),
	)
	server.RegisterDocsRoutes(srv.Router())

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

	cancel() // stop any background processes

	asynqSrv.Shutdown()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("server shutdown error")
	}

	log.Info().Msg("goodbye")
}
