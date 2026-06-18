package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fluxa/fluxa/internal/alerting"
	"github.com/fluxa/fluxa/internal/config"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/indexer"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/settlement"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

	ctx := context.Background()

	db, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("connect to database")
	}
	defer db.Close()

	walletRepo := postgres.NewWalletRepo(db)
	txRepo := postgres.NewTransactionRepo(db)
	feeRepo := postgres.NewFeeRepo(db)
	webhookRepo := postgres.NewWebhookRepo(db)

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

	alertClient := alerting.NewClient(cfg.AlertWebhookURL, "fluxa-worker")
	qClient := queue.NewClient(cfg.RedisURL)

	reconcileSvc := reconcile.NewService(txRepo, stellarClient, alertClient, qClient, "fluxa-worker")
	reconcileWorker := reconcile.NewWorker(reconcileSvc)

	webhookSvc := webhook.NewService(webhookRepo, qClient)
	webhookWorker := webhook.NewWorker(webhookSvc)

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
	mux.HandleFunc(queue.TypeWebhookDeliver, webhookWorker.HandleDeliver)

	scheduler := asynq.NewScheduler(redisOpt, nil)
	syncTask := asynq.NewTask(queue.TypeSyncLedger, nil)
	if _, err := scheduler.Register("@every 30s", syncTask); err != nil {
		log.Fatal().Err(err).Msg("register ledger sync scheduler")
	}
	reconcileTask := asynq.NewTask(queue.TypeReconcile, nil)
	if _, err := scheduler.Register("@every 5m", reconcileTask); err != nil {
		log.Fatal().Err(err).Msg("register reconcile scheduler")
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
	srv.Shutdown()
	scheduler.Shutdown()

	_ = transfer.NewService
	_ = wallet.NewService
}
