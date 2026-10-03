// ── Wallet ──────────────────────────────────────────────────────────────────

export interface CreateWalletResponse {
  id: string;
  public_key: string;
  created_at: string;
}

export interface Balance {
  asset_code: string;
  issuer: string;
  balance: string;
}

export interface GetBalancesResponse {
  wallet_id: string;
  balances: Balance[];
}

export interface CreateTrustlineRequest {
  asset_code: string;
  asset_issuer: string;
  limit?: string;
}

export interface TrustlineResponse {
  wallet_id: string;
  asset_code: string;
  asset_issuer: string;
  status: string;
}

// ── Transaction / Transfer ──────────────────────────────────────────────────

export type TransactionStatus =
  'pending' | 'submitted' | 'confirmed' | 'failed' | 'reconciliation_failed';

export type TransactionType = 'transfer' | 'conversion' | 'funding';

export interface CreateTransferRequest {
  from_wallet_id: string;
  to_wallet_id: string;
  asset: string;
  amount: string;
}

export interface TransferResponse {
  id: string;
  tx_hash?: string;
  type: TransactionType;
  status: TransactionStatus;
  from_wallet_id: string;
  to_wallet_id: string;
  asset: string;
  amount: string;
  fee_amount: string;
  net_amount: string;
  fee_bps: number;
  failure_reason?: string;
  failure_message?: string;
  created_at: string;
}

export interface ListTransactionsQuery {
  wallet_id: string;
  limit?: number;
  offset?: number;
  cursor?: string;
  external_reference?: string;
  tag?: string;
  [key: string]: unknown;
}

export interface ListTransactionsResponse {
  transactions: TransferResponse[];
  next_cursor?: string | null;
  cursor?: string | null;
}

// ── Batch ───────────────────────────────────────────────────────────────────

export type BatchStatus =
  'pending' | 'processing' | 'partial' | 'completed' | 'failed' | 'compliance_hold';

export interface BatchItemRequest {
  to_wallet_id: string;
  asset: string;
  amount: string;
  reference?: string;
}

export interface CreateBatchRequest {
  from_wallet_id: string;
  transfers: BatchItemRequest[];
}

export interface BatchTransferResponse {
  id: string;
  to_wallet_id: string;
  asset: string;
  amount: string;
  reference?: string;
  status: TransactionStatus;
  tx_hash?: string;
  failure_reason?: string;
  failure_message?: string;
}

export interface BatchResponse {
  id: string;
  status: BatchStatus;
  total_count: number;
  success_count: number;
  failed_count: number;
  held_count: number;
  created_at: string;
  transfers?: BatchTransferResponse[];
}

// ── Batch history & preflight ──────────────────────────────────────────────

export interface ListBatchSummary {
  total: number;
  by_status: Record<string, number>;
}

export interface ListBatchesQuery {
  status?: BatchStatus;
  from_wallet?: string;
  limit?: number;
  cursor?: string;
}

export interface ListBatchCursor {
  created_at: string;
  id: string;
}

export interface ListBatchesResponse {
  batches: Array<{
    id: string;
    status: BatchStatus;
    total_count: number;
    created_at: string;
    updated_at: string;
  }>;
  next_cursor?: ListBatchCursor | null;
  summary?: ListBatchSummary;
}

export interface BatchRowResult {
  row: number;
  to_wallet_id: string;
  asset: string;
  amount: string;
  reference?: string;
  valid: boolean;
  error_code?: string;
  error_field?: string;
  error_message?: string;
  estimated_fee?: string;
  net_amount?: string;
}

export interface BatchPreflightResponse {
  total_count: number;
  valid_count: number;
  invalid_count: number;
  estimated_fees: string;
  total_net_amount: string;
  rows: BatchRowResult[];
}

// ── FX ──────────────────────────────────────────────────────────────────────

export interface QuoteRequest {
  from_asset: string;
  to_asset: string;
  amount: string;
}

export interface QuoteResponse {
  id: string;
  org_id: string;
  from_asset: string;
  to_asset: string;
  from_amount: string;
  to_amount: string;
  rate: string;
  fee: string;
  expires_at: string;
  used: boolean;
}

export interface ConvertRequest {
  wallet_id: string;
  quote_id: string;
}

export interface ConversionResponse {
  id: string;
  wallet_id: string;
  source_asset: string;
  dest_asset: string;
  source_amount: string;
  dest_amount: string;
  fee_amount: string;
  fee_bps: number;
  rate: string;
  tx_hash: string;
  created_at: string;
}

export interface RateResponse {
  rate: string;
  mid_market_rate: string;
  spread_bps: number;
  provider: string;
  cached_at: string;
  stale: boolean;
  source_amount: string;
  dest_amount: string;
  fee_amount: string;
  net_amount: string;
  fee_bps: number;
}

export interface GetRatesQuery {
  from: string;
  to: string;
}

// ── Fees ────────────────────────────────────────────────────────────────────

export interface FeeScheduleResponse {
  transfer_fee_bps: number;
  conversion_fee_bps: number;
  min_fee_amount: string;
  max_fee_amount?: string;
  asset: string;
}

export interface FeeCollectionSummary {
  asset: string;
  total_fees: string;
  tenant_fees: TenantFeeTotal[];
}

export interface TenantFeeTotal {
  tenant_id?: string;
  total_fees: string;
}

export interface ListCollectedQuery {
  start_date?: string;
  end_date?: string;
}

export interface ListCollectedResponse {
  summary: FeeCollectionSummary[];
}

// ── Fiat ────────────────────────────────────────────────────────────────────

export interface DepositRequest {
  amount: string;
  currency: string;
  email: string;
  name: string;
}

export interface DepositResponse {
  payment_link: string;
  reference: string;
}

export interface WithdrawRequest {
  amount: string;
  currency: string;
  account_bank: string;
  account_number: string;
}

export interface WithdrawResponse {
  reference: string;
  status: string;
}

export interface CreatePaymentLinkRequest {
  wallet_id: string;
  amount: string;
  currency: string;
  expires_at: string;
}

export interface PaymentLinkResponse {
  id: string;
  token: string;
  wallet_id: string;
  amount: string;
  currency: string;
  status: 'active' | 'processing' | 'paid' | 'failed' | 'cancelled' | 'expired';
  checkout_url: string;
  expires_at: string;
  created_at?: string;
}

export interface PaymentLinksResponse {
  payment_links: PaymentLinkResponse[];
}

export interface CreateRefundRequest {
  original_transaction_id: string;
  amount: string;
  reason?: string;
}

export interface RefundResponse {
  id: string;
  original_transaction_id: string;
  transaction_id?: string;
  amount: string;
  reason?: string;
  status: 'requested' | 'pending' | 'succeeded' | 'failed';
}

export interface RefundsResponse {
  refunds: RefundResponse[];
}

// ── Webhook ─────────────────────────────────────────────────────────────────

export type EventType =
  | 'transfer.initiated'
  | 'transfer.settled'
  | 'transfer.failed'
  | 'wallet.funded'
  | 'conversion.completed';

export type DeliveryStatus = 'pending' | 'success' | 'failed';

export interface RegisterWebhookRequest {
  url: string;
  events?: string[];
}

export interface WebhookEndpointResponse {
  id: string;
  url: string;
  secret?: string;
  events: string[];
  active: boolean;
  created_at: string;
}

export interface ListWebhooksResponse {
  endpoints: WebhookEndpointResponse[];
}

export interface WebhookDeliveryResponse {
  id: string;
  endpoint_id: string;
  event_type: string;
  status: DeliveryStatus;
  response_code?: number;
  attempt_count: number;
  last_attempt?: string;
  created_at: string;
}

export interface ListDeliveriesResponse {
  deliveries: WebhookDeliveryResponse[];
}

export type WebhookSigningSecretStatus = 'active' | 'overlapping' | 'retired';

export interface WebhookSigningSecretMetadata {
  key_id: string;
  created_at: string;
  activated_at: string;
  retired_at?: string | null;
  status: WebhookSigningSecretStatus;
}

export interface ListWebhookSigningSecretsResponse {
  secrets: WebhookSigningSecretMetadata[];
}

export interface RotateWebhookSigningSecretRequest {
  overlap_window_seconds?: number;
}

export interface RotateWebhookSigningSecretResponse extends WebhookSigningSecretMetadata {
  secret: string;
  overlap_window_seconds: number;
}

// ── Schedule ────────────────────────────────────────────────────────────────

export type ScheduleFrequency = 'daily' | 'weekly' | 'monthly';

export type ScheduleStatus =
  'active' | 'processing' | 'failed' | 'paused' | 'cancelled' | 'completed';

export interface CreateScheduleRequest {
  from_wallet_id: string;
  to_wallet_id: string;
  asset: string;
  amount: string;
  frequency: ScheduleFrequency;
  start_date: string;
  end_date?: string;
  timezone?: string;
  missed_run_policy?: 'skip' | 'run_once';
}

export interface UpdateScheduleRequest {
  status?: 'active' | 'paused';
  amount?: string;
  frequency?: ScheduleFrequency;
  end_date?: string;
  timezone?: string;
  missed_run_policy?: 'skip' | 'run_once';
}

export interface ScheduleResponse {
  id: string;
  from_wallet_id: string;
  to_wallet_id: string;
  asset: string;
  amount: string;
  frequency: ScheduleFrequency;
  timezone: string;
  missed_run_policy: 'skip' | 'run_once';
  next_run_at: string;
  end_at?: string;
  status: ScheduleStatus;
  created_at: string;
}

export type ScheduleRunStatus =
  'pending' | 'running' | 'succeeded' | 'failed' | 'skipped' | 'cancelled';

export interface ScheduleRunResponse {
  id: string;
  schedule_id: string;
  expected_run_at: string;
  status: ScheduleRunStatus;
  transaction_id?: string;
  error?: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
}

export interface ListScheduleRunsResponse {
  runs: ScheduleRunResponse[];
}

export interface ListSchedulesResponse {
  schedules: ScheduleResponse[];
}

// ── API Key ─────────────────────────────────────────────────────────────────

/** A resource an API key scope can cover. */
export type APIKeyScopeResource =
  | 'wallets'
  | 'transfers'
  | 'batches'
  | 'webhooks'
  | 'reports'
  | 'keys'
  | 'audit'
  | 'fiat'
  | 'fx'
  | 'fees'
  | 'beneficiaries'
  | 'compliance';

/**
 * A permission an API key can hold: `<resource>:read`, `<resource>:write`,
 * `<resource>:*`, or `*` for everything. Reads (GET) need `read`; anything
 * that changes state needs `write`. A key with no scopes has full access.
 */
export type APIKeyScope = `${APIKeyScopeResource}:${'read' | 'write' | '*'}` | '*' | 'admin';

export interface CreateKeyRequest {
  label?: string;
  /**
   * Scopes to grant. Omit (or pass `[]`) for a full-access key. A scoped key
   * can only create keys with scopes it holds itself.
   */
  scopes?: APIKeyScope[];
  role?: 'owner' | 'admin' | 'developer' | 'viewer';
  mode?: 'live' | 'test';
  expires_at?: string;
  rotation_reminder_days?: number;
}

export interface CreateKeyResponse {
  id: string;
  /** The raw key. Returned only once, in this response. */
  key: string;
  prefix: string;
  label?: string;
  /** Granted scopes; an empty list means full access. */
  scopes: APIKeyScope[];
  created_at: string;
}

export interface APIKeyResponse {
  id: string;
  prefix: string;
  label?: string;
  /** Granted scopes; an empty list means full access. The secret is never listed. */
  scopes: APIKeyScope[];
  last_used_at?: string;
  revoked_at?: string;
  created_at: string;
}

// ── Health ──────────────────────────────────────────────────────────────────

export interface HealthResponse {
  status: string;
  services?: Record<string, string>;
}
