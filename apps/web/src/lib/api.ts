import type {
  APIKey as APIKeyType,
  Balance as WalletBalance,
  BatchResponse,
  BatchTransferRequest,
  Conversion,
  CreateAPIKeyResponse,
  CreateTransferRequest,
  CreateWalletResponse,
  FiatDepositRequest,
  FiatDepositResponse,
  FiatWithdrawRequest,
  FiatWithdrawResponse,
  PaymentLink,
  Refund,
  FxConvertRequest,
  FxQuoteRequest,
  FxQuoteResponse,
  FxRatesResponse,
  ScheduleTransferRequest,
  ScheduleTransferResponse,
  ScheduleRunResponse,
} from './types';
export type { BatchResponse } from './types';
export type { WalletBalance };

export type APIKey = APIKeyType;
export type QuoteResponse = FxQuoteResponse;
export type RateResponse = FxRatesResponse;
export type ScheduleResponse = ScheduleTransferResponse;
export type { ScheduleRunResponse };

export interface StatusResponse {
  api_version: string;
  status: string;
  message: string;
  recent_incidents: Array<{
    id: string;
    title: string;
    description: string;
    severity: string;
    status: string;
    created_at: string;
    resolved_at?: string;
  }>;
}

export interface FeeCollectedSummary {
  collected: Array<{ asset: string; total_fees: string; transfer_count: number }>;
}

export interface Wallet {
  id: string;
  public_key: string;
  created_at: string;
}

export interface WalletWithBalance extends Wallet {
  balances: WalletBalance[];
}

export interface WebhookEndpoint {
  id: string;
  url: string;
  events: string[];
  active: boolean;
}

export interface WebhookDelivery {
  id: string;
  endpoint_id: string;
  status: string;
  status_code: number;
  created_at: string;
}

export interface WebhookEventCatalogEntry {
  name: string;
  description: string;
  example: Record<string, unknown>;
}

export interface HealthResponse {
  status: string;
  services?: Record<string, string>;
}

export interface FeeSchedule {
  transfer_fee_bps: number;
  conversion_fee_bps: number;
  min_fee_amount: string;
  max_fee_amount?: string;
  asset: string;
}

export interface Transaction {
  id: string;
  tx_hash?: string;
  type: string;
  amount: string;
  status: string;
  from_wallet_id: string;
  to_wallet_id: string;
  asset: string;
  fee_amount: string;
  net_amount: string;
  fee_bps: number;
  created_at: string;
  currency?: string;
  batch_id?: string;
  failure_reason?: string;
  failure_message?: string;
}

export interface TransferListParams {
  before?: string;
  after?: string;
  limit?: number;
  sort?: 'created_at' | 'amount' | 'status';
  order?: 'asc' | 'desc';
  status?: string;
  date_from?: string;
  date_to?: string;
  currency?: string;
  batch_id?: string;
}

const API_BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:3000';

/** Sandbox ('test') vs live environment, as selected by the auth context. */
export type EnvironmentMode = 'live' | 'test';

function currentMode(): EnvironmentMode {
  if (typeof window === 'undefined') return 'live';
  return localStorage.getItem('fluxa_mode') === 'test' ? 'test' : 'live';
}

async function request<T>(endpoint: string, options?: RequestInit): Promise<T> {
  const token = typeof window !== 'undefined' ? localStorage.getItem('fluxa_token') : null;
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options?.headers as Record<string, string>),
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(`${API_BASE}${endpoint}`, {
    ...options,
    headers,
  });

  if (!res.ok) {
    const errText = await res.text();
    throw new Error(errText || `API error: ${res.status}`);
  }

  if (res.status === 204) {
    return {} as T;
  }

  return res.json();
}

export const api = {
  listWebhookEvents: (query = '') => {
    const search = new URLSearchParams();
    if (query.trim()) search.set('q', query.trim());
    const encoded = search.toString();
    const suffix = encoded ? `?${encoded}` : '';
    return request<{ events: WebhookEventCatalogEntry[] }>(`/v1/webhooks/events${suffix}`);
  },
  getHealth: () => request<HealthResponse>('/health'),
  getFeeSchedule: () => request<FeeSchedule>('/v1/fees'),
  listWallets: () => request<{ wallets: Wallet[] }>('/v1/wallets'),
  getWalletBalances: (id: string) =>
    request<{ balances: WalletBalance[] }>(`/v1/wallets/${id}/balances`),
  listTransactions: (walletId: string, params: number | TransferListParams = 10) => {
    const query = typeof params === 'number' ? { limit: params } : params;
    const search = new URLSearchParams(
      Object.entries(query)
        .filter(([, value]) => value !== undefined)
        .map(([key, value]) => [key, String(value)]),
    );
    return request<{
      transactions: Transaction[];
      next_cursor?: string;
      has_more?: boolean;
    }>(`/v1/wallets/${walletId}/transactions?${search}`);
  },
  createWallet: () =>
    request<CreateWalletResponse>('/v1/wallets', {
      method: 'POST',
      body: '{}',
    }),
  createTrustline: (walletId: string, body: { asset: string; issuer?: string; limit?: string }) =>
    request(`/v1/wallets/${walletId}/trustlines`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  createTransfer: (body: CreateTransferRequest) =>
    request<Transaction>('/v1/transfers', {
      method: 'POST',
      headers: { 'Idempotency-Key': globalThis.crypto.randomUUID() },
      body: JSON.stringify(body),
    }),
  listAPIKeys: () => request<APIKey[]>('/v1/keys'),
  createAPIKey: (
    params?:
      | string
      | {
          label?: string;
          mode?: EnvironmentMode;
          expires_at?: string;
          rotation_reminder_days?: number;
        },
    mode: EnvironmentMode = currentMode(),
  ) => {
    let body: Record<string, unknown> = {};
    if (typeof params === 'string' || params === undefined) {
      body = { label: params, mode };
    } else {
      body = {
        label: params.label,
        mode: params.mode || mode,
        expires_at: params.expires_at,
        rotation_reminder_days: params.rotation_reminder_days,
      };
    }
    return request<CreateAPIKeyResponse>('/v1/keys', {
      method: 'POST',
      body: JSON.stringify(body),
    });
  },
  rotateAPIKey: (
    id: string,
    body?: { expires_in_days?: number; expires_at?: string; rotation_reminder_days?: number },
  ) =>
    request<CreateAPIKeyResponse>(`/v1/keys/${id}/rotate`, {
      method: 'POST',
      body: JSON.stringify(body || {}),
    }),
  updateAPIKeyExpiry: (
    id: string,
    body: { expires_at?: string | null; rotation_reminder_days?: number },
  ) =>
    request<APIKey>(`/v1/keys/${id}/expiry`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),
  revokeAPIKey: (id: string) => request<void>(`/v1/keys/${id}`, { method: 'DELETE' }),
  getQuote: (body: FxQuoteRequest) =>
    request<QuoteResponse>('/v1/fx/quote', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  getRates: (from: string, to: string) =>
    request<RateResponse>(
      `/v1/fx/rates?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  convert: (body: FxConvertRequest) =>
    request<Conversion>('/v1/fx/convert', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  fiatDeposit: (walletId: string, body: FiatDepositRequest) =>
    request<FiatDepositResponse>(`/v1/wallets/${walletId}/deposit/fiat`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  fiatWithdraw: (walletId: string, body: FiatWithdrawRequest) =>
    request<FiatWithdrawResponse>(`/v1/wallets/${walletId}/withdraw/fiat`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  listPaymentLinks: () => request<{ payment_links: PaymentLink[] }>('/v1/payment-links'),
  createPaymentLink: (body: {
    wallet_id: string;
    amount: string;
    currency: string;
    expires_at: string;
  }) =>
    request<PaymentLink>('/v1/payment-links', {
      method: 'POST',
      headers: { 'Idempotency-Key': globalThis.crypto.randomUUID() },
      body: JSON.stringify(body),
    }),
  cancelPaymentLink: (id: string) => request<void>(`/v1/payment-links/${id}`, { method: 'DELETE' }),
  createRefund: (body: { original_transaction_id: string; amount: string; reason?: string }) =>
    request<Refund>('/v1/refunds', {
      method: 'POST',
      headers: { 'Idempotency-Key': globalThis.crypto.randomUUID() },
      body: JSON.stringify(body),
    }),
  listRefunds: (transactionId: string) =>
    request<{ refunds: Refund[] }>(
      `/v1/refunds?original_transaction_id=${encodeURIComponent(transactionId)}`,
    ),
  listSchedules: () => request<{ schedules: ScheduleResponse[] }>('/v1/schedules'),
  createSchedule: (body: ScheduleTransferRequest) =>
    request<ScheduleResponse>('/v1/schedules', {
      method: 'POST',
      body: JSON.stringify({
        ...body,
        timezone: body.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone,
        missed_run_policy: body.missed_run_policy || 'skip',
      }),
      headers: { 'Idempotency-Key': globalThis.crypto.randomUUID() },
    }),
  listScheduleRuns: (id: string) =>
    request<{ runs: ScheduleRunResponse[] }>(
      `/v1/schedules/${encodeURIComponent(id)}/runs?limit=20`,
    ),
  updateSchedule: (id: string, body: { status?: string }) =>
    request<ScheduleResponse>(`/v1/schedules/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    }),
  cancelSchedule: (id: string) => request<void>(`/v1/schedules/${id}`, { method: 'DELETE' }),
  listFeeCollected: async () => {
    const result = await request<{
      data: Array<{ asset: string; fee_amount: string }>;
    }>('/v1/admin/fees/collected');
    const grouped = new Map<string, { total: number; count: number }>();
    for (const item of result.data) {
      const current = grouped.get(item.asset) || { total: 0, count: 0 };
      current.total += Number(item.fee_amount);
      current.count++;
      grouped.set(item.asset, current);
    }
    return {
      collected: Array.from(grouped, ([asset, summary]) => ({
        asset,
        total_fees: summary.total.toFixed(7),
        transfer_count: summary.count,
      })),
    } satisfies FeeCollectedSummary;
  },
  getStatus: () => request<StatusResponse>('/status'),
  listWebhooks: () => request<{ endpoints: WebhookEndpoint[] }>('/v1/webhooks'),
  registerWebhook: (url: string, events: string[]) =>
    request<WebhookEndpoint>('/v1/webhooks', {
      method: 'POST',
      body: JSON.stringify({ url, events }),
    }),
  deleteWebhook: (id: string) => request<void>(`/v1/webhooks/${id}`, { method: 'DELETE' }),
  listDeliveries: (endpointId: string, limit = 10) =>
    request<{ deliveries: WebhookDelivery[] }>(
      `/v1/webhooks/${endpointId}/deliveries?limit=${limit}`,
    ),
  getWebhookSecret: () => request<{ signing_secret: string }>('/v1/webhooks/secret'),
  rotateWebhookSecret: () =>
    request<{ signing_secret: string }>('/v1/webhooks/secret/rotate', {
      method: 'POST',
    }),
  verifyWebhookSignature: (payload: {
    secret: string;
    timestamp: string;
    body: string;
    signature: string;
  }) =>
    request<{ valid: boolean; reason: string | null }>('/v1/webhooks/verify', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  createBatch: (body: BatchTransferRequest) =>
    request<BatchResponse>('/v1/transfers/batch', {
      method: 'POST',
      headers: { 'Idempotency-Key': globalThis.crypto.randomUUID() },
      body: JSON.stringify(body),
    }),
  getBatch: (id: string) => request<BatchResponse>(`/v1/transfers/batch/${id}`),
  exportBatchCsv: async (id: string) => {
    const token = typeof window !== 'undefined' ? localStorage.getItem('fluxa_token') : null;
    const res = await fetch(`${API_BASE}/v1/transfers/batch/${id}/export`, {
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    });
    if (!res.ok) throw new Error((await res.text()) || `API error: ${res.status}`);
    return res.text();
  },
};
// Add status types and API methods to api.ts
export interface Incident {
  id: string;
  title: string;
  description: string;
  severity: string;
  status: string;
  created_at: string;
  resolved_at?: string;
}

export interface StatusResponse {
  api_version: string;
  status: string;
  message: string;
  recent_incidents: Incident[];
}

// Inside api object:
// getStatus: async (): Promise<StatusResponse> => {
//   const res = await fetch(`${API_URL}/status`);
//   if (!res.ok) throw new Error('Failed to fetch status');
//   return res.json();
// },
// listIncidents: async (): Promise<{ incidents: Incident[] }> => {
//   const res = await fetch(`${API_URL}/status/incidents`);
//   if (!res.ok) throw new Error('Failed to fetch incidents');
//   return res.json();
// },
