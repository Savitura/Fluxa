const BASE_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:3000';

function getToken(): string | null {
  if (typeof window === 'undefined') return null;
  return localStorage.getItem('fluxa_api_key');
}

async function request<T>(
  path: string,
  options: RequestInit = {}
): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string> || {}),
  };
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(`${BASE_URL}${path}`, {
    ...options,
    headers,
  });

  if (res.status === 204) return undefined as T;

  const body = await res.json();

  if (!res.ok) {
    const message = body?.error?.message || body?.message || `Request failed (${res.status})`;
    throw new Error(message);
  }

  return body as T;
}

export interface ComplianceReview {
  id: string;
  transaction_id: string;
  status: 'pending' | 'approved' | 'rejected';
  risk_score: number;
  rules_fired: string[];
  reason?: string;
  reviewed_by?: string;
  review_notes?: string;
  reviewed_at?: string;
  created_at: string;
}

export interface SanctionsStatus {
  loaded: boolean;
  entity_count: number;
  address_count: number;
  name_count: number;
  updated_at?: string;
  last_refresh?: {
    status: 'success' | 'failed';
    entity_count: number;
    duration_ms: number;
    error?: string;
    finished_at: string;
  };
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

export interface Wallet {
  id: string;
  public_key: string;
  created_at: string;
}

export interface WalletBalance {
  asset_code: string;
  issuer: string;
  balance: string;
}

export interface WalletWithBalance extends Wallet {
  balances: WalletBalance[];
}

export interface Transaction {
  id: string;
  tx_hash?: string;
  type: string;
  status: string;
  from_wallet_id: string;
  to_wallet_id: string;
  asset: string;
  amount: string;
  fee_amount: string;
  net_amount: string;
  fee_bps: number;
  created_at: string;
}

export interface CreateTransferRequest {
  from_wallet_id: string;
  to_wallet_id: string;
  asset: string;
  amount: string;
}

export interface APIKey {
  id: string;
  prefix: string;
  label?: string;
  last_used_at?: string;
  revoked_at?: string;
  created_at: string;
}

export interface CreateAPIKeyResponse {
  id: string;
  key: string;
  prefix: string;
  label?: string;
  created_at: string;
}

export interface WebhookEndpoint {
  id: string;
  url: string;
  secret?: string;
  events: string[];
  active: boolean;
  created_at: string;
}

export interface WebhookDelivery {
  id: string;
  endpoint_id: string;
  event_type: string;
  status: string;
  response_code?: number;
  attempt_count: number;
  last_attempt?: string;
  created_at: string;
}

export interface VerifyWebhookSignatureRequest {
  secret: string;
  timestamp: string;
  body: string;
  signature: string;
}

export interface VerifyWebhookSignatureResponse {
  valid: boolean;
  reason: string | null;
}

export interface FeeCollectedSummary {
  collected?: Array<{
    asset: string;
    total_fees: string;
    transfer_count: number;
  }>;
}

// --- FX ---
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

// --- Batch ---
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

export interface BatchResponse {
  id: string;
  status: string;
  total_count: number;
  success_count: number;
  failed_count: number;
  created_at: string;
  transfers?: Array<{
    id: string;
    to_wallet_id: string;
    asset: string;
    amount: string;
    reference?: string;
    status: string;
    tx_hash?: string;
  }>;
}

// --- Schedules ---
export interface CreateScheduleRequest {
  from_wallet_id: string;
  to_wallet_id: string;
  asset: string;
  amount: string;
  frequency: 'daily' | 'weekly' | 'monthly';
  start_date: string;
  end_date?: string;
}

export interface UpdateScheduleRequest {
  status?: 'active' | 'paused';
  amount?: string;
  frequency?: 'daily' | 'weekly' | 'monthly';
  end_date?: string;
}

export interface ScheduleResponse {
  id: string;
  from_wallet_id: string;
  to_wallet_id: string;
  asset: string;
  amount: string;
  frequency: string;
  next_run_at: string;
  end_at?: string;
  status: string;
  created_at: string;
}

// --- Fiat ---
export interface FiatDepositRequest {
  amount: string;
  currency: string;
  email: string;
  name: string;
}

export interface FiatDepositResponse {
  payment_link: string;
  reference: string;
}

export interface FiatWithdrawRequest {
  amount: string;
  currency: string;
  account_bank: string;
  account_number: string;
}

export interface FiatWithdrawResponse {
  reference: string;
  status: string;
}

// --- Trustline ---
export interface CreateTrustlineRequest {
  asset: string;
  issuer?: string;
  limit?: string;
}

export interface TrustlineResponse {
  wallet_id: string;
  asset: string;
  asset_code?: string;
  asset_issuer?: string;
  issuer?: string;
  status: string;
  tx_hash?: string;
}

class FluxaAPI {
  private baseUrl: string;

  constructor() {
    this.baseUrl = BASE_URL;
  }

  // Health
  async getHealth(): Promise<HealthResponse> {
    return request<HealthResponse>('/health');
  }

  // Fees
  async getFeeSchedule(): Promise<FeeSchedule> {
    return request<FeeSchedule>('/v1/fees');
  }

  // Wallets
  async createWallet(): Promise<Wallet> {
    return request<Wallet>('/v1/wallets/', { method: 'POST' });
  }

  async getWalletBalances(walletId: string): Promise<{ wallet_id: string; balances: WalletBalance[] }> {
    return request(`/v1/wallets/${walletId}/balances`);
  }

  // Transfers
  async createTransfer(data: CreateTransferRequest): Promise<Transaction> {
    return request<Transaction>('/v1/transfers/', {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  async getTransaction(id: string): Promise<Transaction> {
    return request<Transaction>(`/v1/transfers/${id}`);
  }

  async listTransactions(walletId: string, limit = 50, offset = 0): Promise<{ transactions: Transaction[] }> {
    return request(`/v1/transactions?wallet_id=${walletId}&limit=${limit}&offset=${offset}`);
  }

  // API Keys
  async listAPIKeys(): Promise<APIKey[]> {
    return request<APIKey[]>('/v1/keys');
  }

  async createAPIKey(label?: string): Promise<CreateAPIKeyResponse> {
    return request<CreateAPIKeyResponse>('/v1/keys/', {
      method: 'POST',
      body: JSON.stringify({ label }),
    });
  }

  async revokeAPIKey(id: string): Promise<void> {
    return request(`/v1/keys/${id}`, { method: 'DELETE' });
  }

  // Webhooks
  async listWebhooks(): Promise<{ endpoints: WebhookEndpoint[] }> {
    return request<{ endpoints: WebhookEndpoint[] }>('/v1/webhooks');
  }

  async registerWebhook(url: string, events: string[]): Promise<WebhookEndpoint> {
    return request<WebhookEndpoint>('/v1/webhooks/', {
      method: 'POST',
      body: JSON.stringify({ url, events }),
    });
  }

  async deleteWebhook(id: string): Promise<void> {
    return request(`/v1/webhooks/${id}`, { method: 'DELETE' });
  }

  async listDeliveries(endpointId: string, limit = 20, offset = 0): Promise<{ deliveries: WebhookDelivery[] }> {
    return request(`/v1/webhooks/${endpointId}/deliveries?limit=${limit}&offset=${offset}`);
  }

  async verifyWebhookSignature(
    data: VerifyWebhookSignatureRequest
  ): Promise<VerifyWebhookSignatureResponse> {
    return request('/v1/webhooks/verify', {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  // Admin - Fee Collected
  async listFeeCollected(startDate?: string, endDate?: string): Promise<FeeCollectedSummary> {
    const params = new URLSearchParams();
    if (startDate) params.set('start_date', startDate);
    if (endDate) params.set('end_date', endDate);
    const qs = params.toString();
    return request(`/v1/admin/fees/collected${qs ? `?${qs}` : ''}`);
  }

  // Wallet details & Trustline
  async getWallet(walletId: string): Promise<Wallet> {
    return request<Wallet>(`/v1/wallets/${walletId}`);
  }

  async createTrustline(
    walletId: string,
    data: CreateTrustlineRequest
  ): Promise<TrustlineResponse> {
    return request<TrustlineResponse>(`/v1/wallets/${walletId}/trustlines`, {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  // FX
  async getQuote(data: QuoteRequest): Promise<QuoteResponse> {
    return request<QuoteResponse>('/v1/fx/quote', {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  async convert(data: ConvertRequest): Promise<ConversionResponse> {
    return request<ConversionResponse>('/v1/fx/convert', {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  async getRates(from: string, to: string): Promise<RateResponse> {
    return request<RateResponse>(`/v1/fx/rates?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`);
  }

  // Batch
  async createBatch(data: CreateBatchRequest): Promise<BatchResponse> {
    return request<BatchResponse>('/v1/transfers/batch/', {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  async getBatch(batchId: string): Promise<BatchResponse> {
    return request<BatchResponse>(`/v1/transfers/batch/${batchId}`);
  }

  async exportBatchCsv(batchId: string): Promise<string> {
    const token = getToken();
    const headers: Record<string, string> = {};
    if (token) headers['Authorization'] = `Bearer ${token}`;
    const res = await fetch(`${BASE_URL}/v1/transfers/batch/${batchId}/export`, { headers });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(body?.error?.message || `Request failed (${res.status})`);
    }
    return res.text();
  }

  // Schedules
  async createSchedule(data: CreateScheduleRequest): Promise<ScheduleResponse> {
    return request<ScheduleResponse>('/v1/schedules/', {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  async listSchedules(): Promise<{ schedules: ScheduleResponse[] }> {
    return request<{ schedules: ScheduleResponse[] }>('/v1/schedules/');
  }

  async updateSchedule(id: string, data: UpdateScheduleRequest): Promise<ScheduleResponse> {
    return request<ScheduleResponse>(`/v1/schedules/${id}`, {
      method: 'PATCH',
      body: JSON.stringify(data),
    });
  }

  async cancelSchedule(id: string): Promise<void> {
    return request(`/v1/schedules/${id}`, { method: 'DELETE' });
  }

  // Fiat
  async fiatDeposit(walletId: string, data: FiatDepositRequest): Promise<FiatDepositResponse> {
    return request<FiatDepositResponse>(`/v1/wallets/${walletId}/deposit/fiat`, {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  async fiatWithdraw(walletId: string, data: FiatWithdrawRequest): Promise<FiatWithdrawResponse> {
    return request<FiatWithdrawResponse>(`/v1/wallets/${walletId}/withdraw/fiat`, {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  // Usage (if backend implements GET /v1/usage, otherwise fallback to derived)
  async getUsage(): Promise<Record<string, unknown>> {
    return request<Record<string, unknown>>('/v1/usage');
  }

  // Reconciliation admin
  async getReconciliationSummary(): Promise<Record<string, unknown>> {
    return request<Record<string, unknown>>('/v1/admin/reconciliation/summary');
  }

  async triggerReconciliation(): Promise<Record<string, unknown>> {
    return request<Record<string, unknown>>('/v1/admin/reconciliation/run', {
      method: 'POST',
    });
  }
  // Compliance admin. Screening runs on the backend before a transfer is
  // enqueued; these endpoints only read and decide reviews.
  async listComplianceReviews(params?: {
    status?: 'pending' | 'approved' | 'rejected';
    limit?: number;
    offset?: number;
  }): Promise<{ reviews: ComplianceReview[] }> {
    const qs = new URLSearchParams();
    if (params?.status) qs.set('status', params.status);
    if (params?.limit !== undefined) qs.set('limit', String(params.limit));
    if (params?.offset !== undefined) qs.set('offset', String(params.offset));
    const query = qs.toString();
    return request<{ reviews: ComplianceReview[] }>(
      `/v1/admin/compliance/reviews${query ? `?${query}` : ''}`
    );
  }

  async getComplianceReview(id: string): Promise<ComplianceReview> {
    return request<ComplianceReview>(`/v1/admin/compliance/reviews/${id}`);
  }

  async approveComplianceReview(id: string, notes?: string): Promise<ComplianceReview> {
    return request<ComplianceReview>(`/v1/admin/compliance/reviews/${id}/approve`, {
      method: 'POST',
      body: JSON.stringify({ notes: notes ?? '' }),
    });
  }

  async rejectComplianceReview(id: string, notes?: string): Promise<ComplianceReview> {
    return request<ComplianceReview>(`/v1/admin/compliance/reviews/${id}/reject`, {
      method: 'POST',
      body: JSON.stringify({ notes: notes ?? '' }),
    });
  }

  async getSanctionsStatus(): Promise<SanctionsStatus> {
    return request<SanctionsStatus>('/v1/admin/compliance/sanctions-status');
  }
}

export const api = new FluxaAPI();
