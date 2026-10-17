package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fluxa/fluxa/internal/alerting"
	"github.com/fluxa/fluxa/internal/assets"
	"github.com/fluxa/fluxa/internal/batch"
	"github.com/fluxa/fluxa/internal/claimable"
	"github.com/fluxa/fluxa/internal/compliance"
	"github.com/fluxa/fluxa/internal/config"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/indexer"
	"github.com/fluxa/fluxa/internal/logging"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/schedule"
	"github.com/fluxa/fluxa/internal/settlement"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/treasury"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

func parseDuration(s string, defaultVal time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("load config")
	}

	logger, err := logging.New(os.Stdout, cfg.LogLevel)
	if err != nil {
		log.Fatal().Err(err).Msg("configure logger")
	}
	log.Logger = logger

	if !cfg.WorkerEnabled {
		log.Info().Msg("worker disabled for this region")
		return
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

	walletRepo := postgres.NewWalletRepo(repoDB)
	txRepo := postgres.NewTransactionRepo(repoDB).WithPrimary(db)
	feeRepo := postgres.NewFeeRepo(repoDB)
	webhookRepo := postgres.NewWebhookRepository(repoDB)
	reconcileRepo := postgres.NewReconcileRepo(repoDB)
	scheduleRepo := postgres.NewScheduleRepo(repoDB)
	treasuryRepo := postgres.NewTreasuryRepo(repoDB)
	complianceRepo := postgres.NewComplianceRepo(repoDB).WithPrimary(db)
	fiatRepo := postgres.NewFiatRepo(repoDB)
	idempotencyRepo := postgres.NewIdempotencyRepo(repoDB)
	batchRepo := postgres.NewBatchRepo(repoDB)

	stellarClient := stellar.NewClient(cfg.StellarLiveHorizonURL, cfg.StellarLiveNetwork, cfg.StellarHorizonTimeout)
	testStellarClient := stellar.NewClient(cfg.StellarTestnetHorizonURL, cfg.StellarTestnetNetwork, cfg.StellarHorizonTimeout)
	clientResolver := stellar.NewModeAwareClients(stellarClient, testStellarClient)
	signer := stellar.NewEnvSigner(cfg.MasterEncryptionKey, cfg.StellarLiveNetwork)
	testSigner := stellar.NewEnvSigner(cfg.MasterEncryptionKey, cfg.StellarTestnetNetwork)
	signerResolver := stellar.NewModeAwareSigners(signer, testSigner)

	feeSvc := fees.NewService(feeRepo)
	engine := settlement.NewEngine(
		txRepo, walletRepo, feeSvc, stellarClient, signer,
		cfg.StellarLiveNetwork, map[string]string{
			"USDC": cfg.StellarUSDCIssuer,
			"EURC": cfg.StellarEURCIssuer,
		}, cfg.PlatformFeeWalletPublicKey,
	).WithClientResolver(clientResolver).WithSignerResolver(signerResolver)
	settlementWorker := settlement.NewWorker(engine)

	idx := indexer.NewWithConfig(walletRepo, txRepo, stellarClient, indexer.Config{
		PaymentsPageLimit: cfg.IndexerPaymentsPageLimit,
		StreamMinBackoff:  parseDuration(cfg.IndexerStreamMinBackoff, 1*time.Second),
		StreamMaxBackoff:  parseDuration(cfg.IndexerStreamMaxBackoff, 30*time.Second),
		SyncPageSize:      cfg.IndexerSyncPageSize,
		StreamConcurrency: cfg.IndexerStreamConcurrency,
		StreamMaxWallets:  cfg.IndexerStreamMaxWallets,
		StreamShardCount:  cfg.IndexerStreamShardCount,
		StreamShardIndex:  cfg.IndexerStreamShardIndex,
	})
	indexerWorker := indexer.NewWorker(idx, cfg)
	metricsMux := http.NewServeMux()
	metricsMux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(w, "# HELP fluxa_indexer_active_streams Active Horizon payment streams.\n# TYPE fluxa_indexer_active_streams gauge\nfluxa_indexer_active_streams %d\n", idx.ActiveStreams())
	})
	metricsServer := &http.Server{Addr: ":" + cfg.IndexerMetricsPort, Handler: metricsMux}
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("indexer metrics server stopped")
		}
	}()

	// StreamAll keeps a live Horizon SSE connection open per wallet so new
	// payments land in the DB in real time; the @every 30s indexer:sync task
	// below is the incremental-poll fallback that also catches up any wallet
	// whose stream is reconnecting.
	go func() {
		if err := idx.StreamAll(ctx); err != nil {
			log.Error().Err(err).Msg("indexer: stream all wallets failed")
		}
	}()

	alertClient := alerting.NewClient(cfg.AlertWebhookURL, "fluxa-worker")
	asynqOpt, err := queue.AsynqRedisOptions(cfg.RedisURL, cfg.RedisSentinelMasterName, cfg.RedisSentinelAddrs, cfg.RedisSentinelPassword)
	if err != nil {
		log.Fatal().Err(err).Msg("configure asynq redis")
	}
	qClient := queue.NewClientWithOptions(asynqOpt)
	defer qClient.Close()
	redisOpt, err := queue.RedisOptions(cfg.RedisURL, cfg.RedisSentinelMasterName, cfg.RedisSentinelAddrs, cfg.RedisSentinelPassword)
	if err != nil {
		log.Fatal().Err(err).Msg("configure redis")
	}
	redisClient := redis.NewUniversalClient(redisOpt)
	defer redisClient.Close()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			if err := redisClient.Set(ctx, "fluxa:worker:heartbeat", time.Now().UTC().Format(time.RFC3339Nano), 30*time.Second).Err(); err != nil {
				log.Warn().Err(err).Msg("worker heartbeat failed")
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()

	webhookSvc := webhook.NewConfigService(webhookRepo, webhookRepo, qClient, cfg.MasterEncryptionKey)
	if err := webhookSvc.(webhook.ConfigService).MigrateLegacySigningSecrets(ctx); err != nil {
		log.Fatal().Err(err).Msg("migrate tenant webhook signing secrets")
	}
	webhookWorker := webhook.NewWorker(webhookSvc)

	// Terminal settlement and reconciliation refresh the parent batch of every
	// batch child they resolve and fire the batch-level webhook on the
	// transition, so batch summaries and batch.* events track live progress.
	batchRecorder := batch.NewRecorder(batchRepo, webhookSvc)
	settlementWorker.WithBatchSyncer(batchRecorder)
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			if err := fiatRepo.CleanupWebhookEvents(ctx); err != nil {
				log.Warn().Err(err).Msg("fiat webhook event cleanup failed")
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Purge expired idempotency records every hour in batches of 1 000 rows.
	// Batching avoids a single large DELETE that could lock the table or spike
	// I/O. The loop drains the full backlog on each tick so that a missed tick
	// (e.g. worker restart) does not leave a growing tail of stale rows.
	go func() {
		const batchSize = 1_000
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		purge := func() {
			for {
				n, err := idempotencyRepo.DeleteExpired(ctx, batchSize)
				if err != nil {
					log.Warn().Err(err).Msg("idempotency records cleanup failed")
					return
				}
				log.Debug().Int64("deleted", n).Msg("idempotency records purge batch")
				if n < batchSize {
					return // backlog drained
				}
			}
		}
		purge() // run once at startup to clear any backlog
		for {
			select {
			case <-ticker.C:
				purge()
			case <-ctx.Done():
				return
			}
		}
	}()

	treasurySvc := treasury.NewService(
		treasuryRepo, stellarClient, nil, webhookSvc,
		cfg.PlatformFeeWalletPublicKey, cfg.StellarNetwork, cfg.TreasurySecretKey,
		cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer,
		treasury.OptionsFromConfig(cfg.TreasuryBaseReserve, cfg.TreasuryReserveCacheTTLSec, cfg.TreasuryReserveConcurrency)...,
	)
	treasuryWorker := treasury.NewWorker(treasurySvc)

	// Claimable balances need the same signer and Horizon client as settlement:
	// creating one spends the org's funds and revoking one claims them back.
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
	claimableWorker := claimable.NewWorker(claimableSvc)

	transferSvc := transfer.NewService(txRepo, walletRepo, feeSvc, qClient)

	// The worker screens too: scheduled payouts run here and go through
	// transfer.initiate() exactly like an API-initiated transfer, so leaving
	// the screener off would let them bypass compliance entirely.
	var complianceWorker *compliance.Worker
	if cfg.ComplianceEnabled {
		sanctionsSet := compliance.NewSanctionsSet()
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

		complianceSvc := compliance.NewService(complianceRepo, screener, sanctionsSet, txRepo, qClient, webhookSvc)
		transferSvc = transfer.ConfigureScreener(transferSvc, complianceSvc)
		complianceWorker = compliance.NewWorker(
			complianceRepo,
			compliance.NewHTTPSDNSource(cfg.OFACSDNURL, nil),
			sanctionsSet,
			webhookSvc,
		)
	}

	scheduleWorker := schedule.NewWorker(scheduleRepo, transferSvc)

	// Use 0 as the balance discrepancy threshold so any deviation is flagged.
	// Override via BALANCE_DISCREPANCY_THRESHOLD env var if needed.
	balanceThreshold := decimal.Zero
	if cfg.BalanceDiscrepancyThreshold != "" {
		if t, err := decimal.NewFromString(cfg.BalanceDiscrepancyThreshold); err == nil {
			balanceThreshold = t
		}
	}

	driftThreshold := reconcile.ParseDriftThreshold(cfg.ReconciliationDriftThresholdUSD)

	reconcileSvc := reconcile.NewService(
		txRepo,
		reconcileRepo,
		walletRepo,
		stellarClient,
		alertClient,
		qClient,
		webhookSvc,
		"fluxa-worker",
		balanceThreshold,
		assets.NewRegistry(cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer),
		cfg.PlatformFeeWalletPublicKey,
	).WithDriftThreshold(driftThreshold).WithBatchSyncer(batchRecorder)
	reconcileWorker := reconcile.NewWorker(reconcileSvc)

	srv := asynq.NewServer(asynqOpt, asynq.Config{

		Concurrency: 10,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
	})

	mux := asynq.NewServeMux()
	mux.Use(logging.WorkerMiddleware(log.Logger))
	mux.HandleFunc(queue.TypeProcessTransfer, settlementWorker.HandleProcessTransfer)
	mux.HandleFunc(queue.TypeSyncLedger, indexerWorker.HandleSyncLedger)
	mux.HandleFunc(queue.TypeReconcile, reconcileWorker.HandleReconcile)
	mux.HandleFunc(queue.TypeBalanceReconcile, reconcileWorker.HandleBalanceReconcile)
	mux.HandleFunc(queue.TypeForceSettle, reconcileWorker.HandleForceSettle)
	mux.HandleFunc(queue.TypeReconcileWallet, reconcileWorker.HandleWalletReconcile)
	mux.HandleFunc(queue.TypeWebhookDeliver, webhookWorker.HandleDeliver)
	mux.HandleFunc(queue.TypeTenantWebhookDeliver, webhookWorker.HandleDeliver)
	mux.HandleFunc(queue.TypeRunSchedules, scheduleWorker.HandleRunSchedules)
	mux.HandleFunc(queue.TypeTreasurySweep, treasuryWorker.HandleSweep)
	mux.HandleFunc(queue.TypeExpireClaimableBalances, claimableWorker.HandleExpiry)
	if complianceWorker != nil {
		mux.HandleFunc(queue.TypeRefreshSanctions, complianceWorker.HandleRefreshSanctions)
	}

	scheduler := asynq.NewScheduler(asynqOpt, nil)

	syncTask := asynq.NewTask(queue.TypeSyncLedger, nil)
	if _, err := scheduler.Register("@every 30s", syncTask); err != nil {
		log.Fatal().Err(err).Msg("register ledger sync scheduler")
	}

	// Reconciliation runs every 5 minutes in the low-priority queue so it does
	// not compete with live settlement tasks.
	reconcileTask := asynq.NewTask(queue.TypeReconcile, nil, asynq.Queue("low"))
	if _, err := scheduler.Register("@every 5m", reconcileTask); err != nil {
		log.Fatal().Err(err).Msg("register reconcile scheduler")
	}

	// Balance drift snapshots are refreshed hourly; discrepancies are flagged
	// only ΓÇö never auto-corrected.
	balanceTask := asynq.NewTask(queue.TypeBalanceReconcile, nil, asynq.Queue("low"))
	if _, err := scheduler.Register("@every 1h", balanceTask); err != nil {
		log.Fatal().Err(err).Msg("register balance reconcile scheduler")
	}

	// Scheduled payouts are checked every minute ΓÇö matches the acceptance
	// window (fires within ┬▒1 minute of next_run_at) without needing a
	// dedicated ticker.
	scheduleTask := asynq.NewTask(queue.TypeRunSchedules, nil)
	if _, err := scheduler.Register("@every 1m", scheduleTask); err != nil {
		log.Fatal().Err(err).Msg("register schedule run scheduler")
	}

	// Treasury sweep runs once a day; assets with auto_sweep_enabled = false
	// are skipped by the worker itself, so disabling sweeping is effective
	// immediately without touching this schedule.
	treasurySweepTask := asynq.NewTask(queue.TypeTreasurySweep, nil, asynq.Queue("low"))
	if _, err := scheduler.Register("@daily", treasurySweepTask); err != nil {
		log.Fatal().Err(err).Msg("register treasury sweep scheduler")
	}

	// The expiry tracker runs every 5 minutes so an unclaimed balance is marked
	// expired (and, when revoke_on_expiry is set, claimed back to the org)
	// within 10 minutes of its expires_at.
	claimableExpiryTask := asynq.NewTask(queue.TypeExpireClaimableBalances, nil)
	if _, err := scheduler.Register("@every 5m", claimableExpiryTask); err != nil {
		log.Fatal().Err(err).Msg("register claimable balance expiry scheduler")
	}

	// The OFAC SDN list is republished on business days; a daily refresh on the
	// low queue keeps every process's in-memory set current via
	// sanctions_entities without competing with live settlement.
	if complianceWorker != nil {
		sanctionsTask := asynq.NewTask(queue.TypeRefreshSanctions, nil, asynq.Queue("low"))
		if _, err := scheduler.Register("@daily", sanctionsTask); err != nil {
			log.Fatal().Err(err).Msg("register sanctions refresh scheduler")
		}
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := scheduler.Run(); err != nil {
			log.Error().Err(err).Msg("scheduler error")
		}
	}()

	go func() {
		log.Info().Msg("fluxa worker starting")
		if err := srv.Run(mux); err != nil {
			log.Error().Err(err).Msg("worker stopped")
		}
	}()

	<-quit
	log.Info().Msg("worker shutting down")
	cancel() // stop indexer payment streams
	srv.Shutdown()
	scheduler.Shutdown()
	metricsCtx, metricsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer metricsCancel()
	if err := metricsServer.Shutdown(metricsCtx); err != nil {
		log.Error().Err(err).Msg("indexer metrics server shutdown")
	}
}
