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

export interface HealthResponse {
  status: string;
  services?: Record<string, string>;
}

export interface FeeSchedule {
  transfer_fee_bps: number;
  conversion_fee_bps: number;
  min_fee_amount: string;
}

export interface Transaction {
  id: string;
  amount: string;
  status: string;
  created_at: string;
  currency?: string;
  batch_id?: string;
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

export interface WalletBalance {
  asset: string;
  balance: string;
}

const API_BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:3000';

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
  getHealth: () => request<HealthResponse>('/health'),
  getFeeSchedule: () => request<FeeSchedule>('/v1/fees'),
  listWallets: () => request<{ wallets: any[] }>('/v1/wallets'),
  getWalletBalances: (id: string) => request<{ balances: WalletBalance[] }>(`/v1/wallets/${id}/balances`),
  listTransactions: (walletId: string, params: number | TransferListParams = 10) => {
    const query = typeof params === 'number' ? { limit: params } : params;
    const search = new URLSearchParams(Object.entries(query).filter(([, value]) => value !== undefined).map(([key, value]) => [key, String(value)]));
    return request<{ transactions: Transaction[]; next_cursor?: string; has_more?: boolean }>(`/v1/wallets/${walletId}/transactions?${search}`);
  },
  listWebhooks: () => request<{ endpoints: WebhookEndpoint[] }>('/v1/webhooks'),
  registerWebhook: (url: string, events: string[]) => request<WebhookEndpoint>('/v1/webhooks', {
    method: 'POST',
    body: JSON.stringify({ url, events }),
  }),
  deleteWebhook: (id: string) => request<void>(`/v1/webhooks/${id}`, { method: 'DELETE' }),
  listDeliveries: (endpointId: string, limit = 10) => request<{ deliveries: WebhookDelivery[] }>(`/v1/webhooks/${endpointId}/deliveries?limit=${limit}`),
  getWebhookSecret: () => request<{ signing_secret: string }>('/v1/webhooks/secret'),
  rotateWebhookSecret: () => request<{ signing_secret: string }>('/v1/webhooks/secret/rotate', { method: 'POST' }),
  verifyWebhookSignature: (payload: { secret: string; timestamp: string; body: string; signature: string }) => request<{ valid: boolean; reason: string | null }>('/v1/webhooks/verify', {
    method: 'POST',
    body: JSON.stringify(payload),
  }),
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
