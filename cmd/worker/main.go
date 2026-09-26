package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fluxa/fluxa/internal/alerting"
	"github.com/fluxa/fluxa/internal/assets"
	"github.com/fluxa/fluxa/internal/config"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/indexer"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/schedule"
	"github.com/fluxa/fluxa/internal/settlement"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/treasury"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

func main() {
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

	db, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("connect to database")
	}
	defer db.Close()

	walletRepo := postgres.NewWalletRepo(db)
	txRepo := postgres.NewTransactionRepo(db)
	feeRepo := postgres.NewFeeRepo(db)
	webhookRepo := postgres.NewWebhookRepo(db)
	reconcileRepo := postgres.NewReconcileRepo(db)
	scheduleRepo := postgres.NewScheduleRepo(db)
	treasuryRepo := postgres.NewTreasuryRepo(db)

	stellarClient := stellar.NewClient(cfg.StellarHorizonURL, cfg.StellarNetwork)
	signer := stellar.NewEnvSigner(cfg.MasterEncryptionKey, cfg.StellarNetwork)

	feeSvc := fees.NewService(feeRepo)
	engine := settlement.NewEngine(
		txRepo, walletRepo, feeSvc, stellarClient, signer,
		cfg.StellarNetwork, cfg.StellarUSDCIssuer, cfg.PlatformFeeWalletPublicKey,
	)
	settlementWorker := settlement.NewWorker(engine)

	idx := indexer.New(walletRepo, txRepo, stellarClient)
	indexerWorker := indexer.NewWorker(idx)

	// StreamAll keeps a live Horizon SSE connection open per wallet so new
	// payments land in the DB in real time; the @every 30s indexer:sync task
	// below is the incremental-poll fallback that also catches up any wallet
	// whose stream is reconnecting.
	go func() {
		if err := idx.StreamAll(ctx, 1000, 0); err != nil {
			log.Error().Err(err).Msg("indexer: stream all wallets failed")
		}
	}()

	alertClient := alerting.NewClient(cfg.AlertWebhookURL, "fluxa-worker")
	qClient := queue.NewClient(cfg.RedisURL)

	webhookSvc := webhook.NewConfigService(webhookRepo, webhookRepo, qClient)
	webhookWorker := webhook.NewWorker(webhookSvc)

	treasurySvc := treasury.NewService(
		treasuryRepo, stellarClient, nil, webhookSvc,
		cfg.PlatformFeeWalletPublicKey, cfg.StellarNetwork, cfg.TreasurySecretKey,
		cfg.StellarUSDCIssuer, cfg.StellarEURCIssuer,
	)
	treasuryWorker := treasury.NewWorker(treasurySvc)

	transferSvc := transfer.NewService(txRepo, walletRepo, feeSvc, qClient)
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
	).WithDriftThreshold(driftThreshold)
	reconcileWorker := reconcile.NewWorker(reconcileSvc)

	redisOpt, _ := asynq.ParseRedisURI(cfg.RedisURL)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 10,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeProcessTransfer, settlementWorker.HandleProcessTransfer)
	mux.HandleFunc(queue.TypeSyncLedger, indexerWorker.HandleSyncLedger)
	mux.HandleFunc(queue.TypeReconcile, reconcileWorker.HandleReconcile)
	mux.HandleFunc(queue.TypeBalanceReconcile, reconcileWorker.HandleBalanceReconcile)
	mux.HandleFunc(queue.TypeWebhookDeliver, webhookWorker.HandleDeliver)
	mux.HandleFunc(queue.TypeTenantWebhookDeliver, webhookWorker.HandleDeliver)
	mux.HandleFunc(queue.TypeRunSchedules, scheduleWorker.HandleRunSchedules)
	mux.HandleFunc(queue.TypeTreasurySweep, treasuryWorker.HandleSweep)

	scheduler := asynq.NewScheduler(redisOpt, nil)

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

	_ = wallet.NewService
}
