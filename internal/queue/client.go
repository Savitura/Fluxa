package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

type Client struct {
	inner *asynq.Client
}

func NewClient(redisURL string) *Client {
	return NewClientWithOptions(MustRedisOptions(redisURL, "", nil, ""))
}

func NewClientWithOptions(opt asynq.RedisConnOpt) *Client {
	return &Client{inner: asynq.NewClient(opt)}
}

func MustRedisOptions(redisURL, master string, addrs []string, password string) asynq.RedisConnOpt {
	opt, err := AsynqRedisOptions(redisURL, master, addrs, password)
	if err != nil {
		panic(err)
	}
	return opt
}

func (c *Client) Close() error {
	return c.inner.Close()
}

func (c *Client) EnqueueTransfer(ctx context.Context, txID string) error {
	payload, err := json.Marshal(ProcessTransferPayload{TransactionID: txID})
	if err != nil {
		return fmt.Errorf("marshal transfer payload: %w", err)
	}
	task := asynq.NewTask(TypeProcessTransfer, payload)
	_, err = c.inner.EnqueueContext(ctx, task,
		asynq.MaxRetry(5),
		asynq.Queue("critical"),
	)
	return err
}

func (c *Client) EnqueueLedgerSync(ctx context.Context, walletID, cursor string) error {
	payload, err := json.Marshal(SyncLedgerPayload{WalletID: walletID, Cursor: cursor})
	if err != nil {
		return fmt.Errorf("marshal sync payload: %w", err)
	}
	task := asynq.NewTask(TypeSyncLedger, payload)
	_, err = c.inner.EnqueueContext(ctx, task,
		asynq.MaxRetry(3),
		asynq.Queue("default"),
	)
	return err
}

// EnqueueSanctionsRefresh triggers an out-of-band OFAC SDN refresh. The daily
// run is registered on the scheduler; this exists for manual re-runs. It uses
// the low queue so a refresh never competes with live settlement.
func (c *Client) EnqueueSanctionsRefresh(ctx context.Context) error {
	task := asynq.NewTask(TypeRefreshSanctions, nil)
	_, err := c.inner.EnqueueContext(ctx, task,
		asynq.MaxRetry(3),
		asynq.Queue("low"),
	)
	return err
}

func (c *Client) EnqueueWebhookDelivery(ctx context.Context, deliveryID string) error {
	payload, err := json.Marshal(WebhookDeliverPayload{DeliveryID: deliveryID})
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}
	task := asynq.NewTask(TypeWebhookDeliver, payload)
	_, err = c.inner.EnqueueContext(ctx, task,
		asynq.MaxRetry(5),
		asynq.Queue("default"),
	)
	return err
}
