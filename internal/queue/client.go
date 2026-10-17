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

func NewClientWithOptions(opt asynq.RedisConnOpt) *Client {
	return &Client{inner: asynq.NewClient(opt)}
}

func (c *Client) Close() error {
	return c.inner.Close()
}

func (c *Client) EnqueueTransfer(ctx context.Context, txID string) error {
	return c.EnqueueBatchTransfer(ctx, txID, "")
}

// EnqueueBatchTransfer queues settlement for one transfer of a batch, with the
// batch id attached to the task payload as required by the batch contract.
// An empty batchID produces the plain transfer task.
func (c *Client) EnqueueBatchTransfer(ctx context.Context, txID, batchID string) error {
	payload, err := json.Marshal(ProcessTransferPayload{
		TransactionID: txID,
		BatchID:       batchID,
		Trace:         traceContext(ctx),
	})
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

// EnqueueWebhookDelivery enqueues a webhook delivery job. Options such as
// asynq.ProcessIn let the webhook service schedule backoff retries without a
// worker-side sleep.
func (c *Client) EnqueueWebhookDelivery(ctx context.Context, deliveryID string, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	payload, err := json.Marshal(WebhookDeliverPayload{
		DeliveryID: deliveryID,
		Trace:      traceContext(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook payload: %w", err)
	}
	task := asynq.NewTask(TypeWebhookDeliver, payload)
	options := append([]asynq.Option{asynq.MaxRetry(5), asynq.Queue("default")}, opts...)
	return c.inner.EnqueueContext(ctx, task, options...)
}

func (c *Client) EnqueueTenantWebhookDelivery(ctx context.Context, deliveryID, tenantID string) error {
	payload, err := json.Marshal(WebhookDeliverPayload{
		DeliveryID: deliveryID,
		TenantID:   tenantID,
		Config:     true,
		Trace:      traceContext(ctx),
	})
	if err != nil {
		return fmt.Errorf("marshal tenant webhook payload: %w", err)
	}
	task := asynq.NewTask(TypeTenantWebhookDeliver, payload)
	_, err = c.inner.EnqueueContext(ctx, task,
		asynq.MaxRetry(5),
		asynq.Queue("default"),
	)
	return err
}

// Enqueue pushes an arbitrary task type onto the default queue. It exists
// for low-traffic admin actions (force-settle, one-off wallet reconciliation)
// that do not warrant a dedicated method.
func (c *Client) Enqueue(ctx context.Context, taskType string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", taskType, err)
	}
	task := asynq.NewTask(taskType, body)
	_, err = c.inner.EnqueueContext(ctx, task, asynq.MaxRetry(3), asynq.Queue("default"))
	return err
}
