package stellar

import (
	"context"

	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ContextClient is the context-aware extension of Client. *horizonClient
// implements it; test doubles and alternative clients that only satisfy Client
// keep working because every helper below falls back to the plain method.
type ContextClient interface {
	LoadAccountWithContext(context.Context, string) (horizon.Account, error)
	SubmitTransactionWithContext(context.Context, *txnbuild.Transaction) (horizon.Transaction, error)
	FindPathsStrictWithContext(context.Context, string, string, string, string) ([]horizon.Path, error)
	TransactionDetailWithContext(context.Context, string) (horizon.Transaction, error)
	OperationsForTransactionWithContext(context.Context, string) ([]operations.Operation, error)
	PaymentsWithContext(context.Context, string, string, uint) ([]operations.Operation, error)
	StreamPaymentsWithContext(context.Context, string, string, func(operations.Operation) error) error
	OffersWithContext(context.Context, string, uint) ([]horizon.Offer, error)
}

// The *WithContext helpers below wrap each Horizon call in a child span so
// Horizon shows up as a child of the originating request or job. They delegate
// to the plain Client methods rather than reimplementing them, so error
// wrapping stays byte-for-byte identical to the untraced path ΓÇö call sites rely
// on type-asserting the returned error to *horizonclient.Error to detect 404s.

func LoadAccountWithContext(ctx context.Context, client Client, accountID string) (horizon.Account, error) {
	if contextClient, ok := client.(ContextClient); ok {
		return contextClient.LoadAccountWithContext(ctx, accountID)
	}
	return client.LoadAccount(accountID)
}

func SubmitTransactionWithContext(ctx context.Context, client Client, tx *txnbuild.Transaction) (horizon.Transaction, error) {
	if contextClient, ok := client.(ContextClient); ok {
		return contextClient.SubmitTransactionWithContext(ctx, tx)
	}
	return client.SubmitTransaction(tx)
}

func FindPathsStrictWithContext(ctx context.Context, client Client, sourceAccount, destAsset, destIssuer, destAmount string) ([]horizon.Path, error) {
	if contextClient, ok := client.(ContextClient); ok {
		return contextClient.FindPathsStrictWithContext(ctx, sourceAccount, destAsset, destIssuer, destAmount)
	}
	return client.FindPathsStrict(sourceAccount, destAsset, destIssuer, destAmount)
}

func TransactionDetailWithContext(ctx context.Context, client Client, hash string) (horizon.Transaction, error) {
	if contextClient, ok := client.(ContextClient); ok {
		return contextClient.TransactionDetailWithContext(ctx, hash)
	}
	return client.TransactionDetail(hash)
}

func OperationsForTransactionWithContext(ctx context.Context, client Client, hash string) ([]operations.Operation, error) {
	if contextClient, ok := client.(ContextClient); ok {
		return contextClient.OperationsForTransactionWithContext(ctx, hash)
	}
	return client.OperationsForTransaction(hash)
}

func PaymentsWithContext(ctx context.Context, client Client, accountID, cursor string, limit uint) ([]operations.Operation, error) {
	if contextClient, ok := client.(ContextClient); ok {
		return contextClient.PaymentsWithContext(ctx, accountID, cursor, limit)
	}
	return client.Payments(accountID, cursor, limit)
}

func StreamPaymentsWithContext(ctx context.Context, client Client, accountID, cursor string, handler func(operations.Operation) error) error {
	if contextClient, ok := client.(ContextClient); ok {
		return contextClient.StreamPaymentsWithContext(ctx, accountID, cursor, handler)
	}
	return client.StreamPayments(ctx, accountID, cursor, handler)
}

func OffersWithContext(ctx context.Context, client Client, accountID string, limit uint) ([]horizon.Offer, error) {
	if contextClient, ok := client.(ContextClient); ok {
		return contextClient.OffersWithContext(ctx, accountID, limit)
	}
	return client.Offers(accountID, limit)
}

func (c *horizonClient) LoadAccountWithContext(ctx context.Context, accountID string) (horizon.Account, error) {
	_, span := tracing.Start(ctx, "stellar.horizon.load_account",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("stellar.account_id", accountID)),
	)
	defer span.End()
	return c.LoadAccount(accountID)
}

func (c *horizonClient) SubmitTransactionWithContext(ctx context.Context, tx *txnbuild.Transaction) (horizon.Transaction, error) {
	_, span := tracing.Start(ctx, "stellar.horizon.submit_transaction", trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()
	return c.SubmitTransaction(tx)
}

func (c *horizonClient) TransactionDetailWithContext(ctx context.Context, hash string) (horizon.Transaction, error) {
	_, span := tracing.Start(ctx, "stellar.horizon.transaction_detail",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("stellar.transaction_hash", hash)),
	)
	defer span.End()
	return c.TransactionDetail(hash)
}

func (c *horizonClient) OperationsForTransactionWithContext(ctx context.Context, hash string) ([]operations.Operation, error) {
	_, span := tracing.Start(ctx, "stellar.horizon.operations",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("stellar.transaction_hash", hash)),
	)
	defer span.End()
	return c.OperationsForTransaction(hash)
}

func (c *horizonClient) PaymentsWithContext(ctx context.Context, accountID, cursor string, limit uint) ([]operations.Operation, error) {
	_, span := tracing.Start(ctx, "stellar.horizon.payments",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("stellar.account_id", accountID)),
	)
	defer span.End()
	return c.Payments(accountID, cursor, limit)
}

func (c *horizonClient) StreamPaymentsWithContext(ctx context.Context, accountID, cursor string, handler func(operations.Operation) error) error {
	_, span := tracing.Start(ctx, "stellar.horizon.stream_payments",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("stellar.account_id", accountID)),
	)
	defer span.End()
	return c.StreamPayments(ctx, accountID, cursor, handler)
}

func (c *horizonClient) OffersWithContext(ctx context.Context, accountID string, limit uint) ([]horizon.Offer, error) {
	_, span := tracing.Start(ctx, "stellar.horizon.offers",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("stellar.account_id", accountID)),
	)
	defer span.End()
	return c.Offers(accountID, limit)
}

func (c *horizonClient) FindPathsStrictWithContext(ctx context.Context, sourceAccount, destAsset, destIssuer, destAmount string) ([]horizon.Path, error) {
	_, span := tracing.Start(ctx, "stellar.horizon.find_paths",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("stellar.source_account", sourceAccount)),
	)
	defer span.End()
	return c.FindPathsStrict(sourceAccount, destAsset, destIssuer, destAmount)
}

var _ ContextClient = (*horizonClient)(nil)
