package reconcile

import (
    "context"
    "encoding/json"

    "github.com/hibiken/asynq"
    "github.com/rs/zerolog/log"
)

type Worker struct {
	service *Service
}

func NewWorker(service *Service) *Worker {
	return &Worker{service: service}
}

// HandleReconcile runs the full pending + confirmed reconciliation pass.
// Registered as a periodic Asynq task every 5 minutes.
func (w *Worker) HandleReconcile(ctx context.Context, _ *asynq.Task) error {
    log.Info().Msg("reconcile: scheduled run starting")
    if err := w.service.RunAll(ctx); err != nil {
        log.Error().Err(err).Msg("reconcile: scheduled run failed")
        return err
    }
    log.Info().Msg("reconcile: scheduled run complete")
    return nil
}

// HandleBalanceReconcile runs the daily balance reconciliation job.
// It compares DB balances against live Horizon account balances and flags
// discrepancies -- never auto-corrects.
func (w *Worker) HandleBalanceReconcile(ctx context.Context, _ *asynq.Task) error {
    log.Info().Msg("reconcile: balance reconciliation starting")
    if err := w.service.RunBalanceReconciliation(ctx); err != nil {
        log.Error().Err(err).Msg("reconcile: balance reconciliation failed")
        return err
    }
    log.Info().Msg("reconcile: balance reconciliation complete")
    return nil
}
// HandleWalletReconcile runs a one-off reconciliation for a specific wallet.
// It is triggered by an admin action and runs through the worker queue.
func (w *Worker) HandleWalletReconcile(ctx context.Context, t *asynq.Task) error {
    var payload WalletReconcilePayload
    if err := json.Unmarshal(t.Payload(), &payload); err != nil {
        log.Error().Err(err).Msg("reconcile: invalid wallet reconcile payload")
        return err
    }
    log.Info().Str("wallet_id", payload.WalletID).Str("actor", payload.Actor).Msg("reconcile: wallet reconcile starting")
    if err := w.service.RunWalletReconciliation(ctx, payload.WalletID, payload.Actor); err != nil {
        log.Error().Err(err).Str("wallet_id", payload.WalletID).Msg("reconcile: wallet reconcile failed")
        return err
    }
    log.Info().Str("wallet_id", payload.WalletID).Msg("reconcile: wallet reconcile complete")
    return nil
}

// HandleForceSettle runs a force settlement for a specific transfer.
// It is triggered by an admin action and runs through the worker queue.
func (w *Worker) HandleForceSettle(ctx context.Context, t *asynq.Task) error {
    var payload ForceSettlePayload
    if err := json.Unmarshal(t.Payload(), &payload); err != nil {
        log.Error().Err(err).Msg("reconcile: invalid force settle payload")
        return err
    }
    log.Info().Str("transfer_id", payload.TransferID).Str("actor", payload.Actor).Msg("reconcile: force settle starting")
    if err := w.service.ForceSettle(ctx, payload.TransferID, payload.Actor); err != nil {
        log.Error().Err(err).Str("transfer_id", payload.TransferID).Msg("reconcile: force settle failed")
        return err
    }
    log.Info().Str("transfer_id", payload.TransferID).Msg("reconcile: force settle complete")
    return nil
}

// WalletReconcilePayload is the Asynq task payload for a one-off wallet reconciliation.
type WalletReconcilePayload struct {
	WalletID string `son:"wallet_id"`
	Actor    string `json:"actor"`
}

// ForceSettlePayload is the Asynq task payload for a force settlement.
type ForceSettlePayload struct {
	TransferID string `json:"transfer_id"`
	Actor    string `json:"actor"`
}
