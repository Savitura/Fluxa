package domain

// This file is the single source of truth for the webhook event contract.
//
// Before it existed, three lists were maintained by hand and had drifted:
// the event constants above, SupportedEventTypes, and the HTTP event catalogue
// in internal/webhook/event_catalog.go. The catalogue advertised five events
// that RegisterEndpoint rejected with a 400, and omitted ten events that a
// customer could legitimately subscribe to. Every example payload below is
// copied from the call site that actually dispatches the event, so the
// catalogue describes what the platform sends rather than what someone
// remembered it sending.

// WebhookEvent is one entry in the webhook event contract: the name a customer
// subscribes to, the one-line summary the catalogue endpoint returns, and an
// example payload drawn from the real dispatch site.
type WebhookEvent struct {
	Name        string
	Description string
	Example     string
}

// WebhookEvents lists every event a customer may subscribe to. Adding an entry
// here makes an event simultaneously subscribable and advertised, which is why
// SupportedEventTypes and the event catalogue both derive from this slice
// instead of restating it.
var WebhookEvents = []WebhookEvent{
	{
		Name:        EventTransferSettled,
		Description: "A transfer has been confirmed on Stellar.",
		Example:     `{"transaction_id":"txn_123","event":"transfer.settled","tx_hash":"abc123","amount":"25.00","asset":"USDC"}`,
	},
	{
		Name:        EventTransferFailed,
		Description: "A transfer failed before settlement completed.",
		Example:     `{"transaction_id":"txn_123","event":"transfer.failed","amount":"25.00","asset":"USDC"}`,
	},
	{
		Name:        EventWalletFunded,
		Description: "A wallet received funds.",
		Example:     `{"wallet_id":"wal_123","asset":"USDC","amount":"25.00","transaction_hash":"abc123"}`,
	},
	{
		Name:        EventTreasurySweepCompleted,
		Description: "A treasury sweep to an external destination completed.",
		Example:     `{"asset":"USDC","amount":"100.00","destination":"GABC...","tx_hash":"abc123","triggered_by":"manual","swept_at":"2026-01-15T10:00:00Z"}`,
	},
	{
		Name:        EventReconciliationDrift,
		Description: "A wallet's on-chain balance drifted from the recorded balance.",
		Example:     `{"tenant_id":"tenant_1","wallet_id":"wal_123","wallet_address":"GABC...","asset":"USDC","expected_balance":"100.00","actual_balance":"99.00","drift_amount":"1.00","threshold":"0.01","detected_at":"2026-01-15T10:00:00Z"}`,
	},
	{
		Name:        EventTransferComplianceHold,
		Description: "Compliance placed a hold on a transfer pending review.",
		Example:     `{"review_id":"rev_123","transaction_id":"txn_123","status":"pending","rules_fired":["sanctions_match"],"review_notes":""}`,
	},
	{
		Name:        EventTransferComplianceApproved,
		Description: "A compliance hold was approved and the transfer resumed.",
		Example:     `{"review_id":"rev_123","transaction_id":"txn_123","status":"approved","rules_fired":["sanctions_match"],"review_notes":"cleared by analyst"}`,
	},
	{
		Name:        EventTransferComplianceRejected,
		Description: "A compliance hold was rejected and the transfer was stopped.",
		Example:     `{"review_id":"rev_123","transaction_id":"txn_123","status":"rejected","rules_fired":["sanctions_match"],"review_notes":"confirmed match"}`,
	},
	{
		Name:        EventBatchCompleted,
		Description: "Every transfer in a batch reached a terminal state (check status for the outcome).",
		Example:     `{"batch_id":"bat_123","event":"batch.completed","status":"completed","total_count":10,"succeeded_count":10,"failed_count":0}`,
	},
	{
		Name:        EventBatchPartiallyFailed,
		Description: "A batch finished with at least one succeeded and at least one failed transfer.",
		Example:     `{"batch_id":"bat_123","event":"batch.partially_failed","status":"partial","total_count":10,"succeeded_count":9,"failed_count":1}`,
	},
	{
		Name:        EventSanctionsRefreshFailed,
		Description: "The scheduled sanctions list refresh failed.",
		Example:     `{"source":"ofac","error":"upstream timeout","duration_ms":4200,"failed_at":"2026-01-15T10:00:00Z"}`,
	},
	{
		Name:        EventClaimableBalanceCreated,
		Description: "A claimable balance was created and is awaiting a claimant.",
		Example:     `{"balance_id":"cb_123","asset":"USDC","amount":"25.00","sponsor":"GABC...","tx_hash":"abc123","expires_at":"2026-02-15T10:00:00Z"}`,
	},
	{
		Name:        EventClaimableBalanceClaimed,
		Description: "A claimable balance was claimed by its recipient.",
		Example:     `{"balance_id":"cb_123","asset":"USDC","amount":"25.00","claimed_by":"GXYZ...","tx_hash":"abc123"}`,
	},
	{
		Name:        EventClaimableBalanceExpired,
		Description: "A claimable balance expired unclaimed and was reclaimed.",
		Example:     `{"balance_id":"cb_123","asset":"USDC","amount":"25.00","expired_at":"2026-02-15T10:00:00Z"}`,
	},
	{
		Name:        EventClaimableBalanceRevoked,
		Description: "A claimable balance was revoked by its sponsor before it was claimed.",
		Example:     `{"balance_id":"cb_123","asset":"USDC","amount":"25.00","revoked_by":"GABC...","tx_hash":"abc123"}`,
	},
	{
		Name:        EventAPIKeyRotationReminder,
		Description: "An API key is approaching its configured expiry.",
		Example:     `{"key_id":"key_123","prefix":"sk_live_ab","label":"production","mode":"live","role":"admin","expires_at":"2030-01-01T00:00:00Z","days_until_expiration":14,"rotation_reminder_days":14,"reminded_at":"2026-12-18T00:00:00Z"}`,
	},
	{
		Name:        EventAPIKeyExpired,
		Description: "An API key has expired.",
		Example:     `{"key_id":"key_123","expired_at":"2030-01-01T00:00:00Z"}`,
	},
}

// SupportedEventTypes lists the event names a customer may subscribe to.
//
// It is derived from WebhookEvents so that a new event becomes subscribable and
// advertised in the same change. The order follows WebhookEvents, which the
// catalogue endpoint re-sorts alphabetically before returning.
var SupportedEventTypes = func() []string {
	names := make([]string, len(WebhookEvents))
	for i, event := range WebhookEvents {
		names[i] = event.Name
	}
	return names
}()

// supportedEventSet backs IsSupportedEventType. Building it once keeps the
// validation on the request path a map lookup rather than a linear scan over
// the slice, which matters because it runs on every registration.
var supportedEventSet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(WebhookEvents))
	for _, event := range WebhookEvents {
		set[event.Name] = struct{}{}
	}
	return set
}()

func IsSupportedEventType(eventType string) bool {
	_, ok := supportedEventSet[eventType]
	return ok
}
