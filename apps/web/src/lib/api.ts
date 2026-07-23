import {
  Wallet,
  WalletBalances,
  Transaction,
  TransactionsResponse,
  CreateTransferRequest,
  FxQuoteRequest,
  FxQuoteResponse,
  FxConvertRequest,
  Conversion,
  FxRatesResponse,
  APIKey,
  CreateAPIKeyResponse,
  WebhookEndpoint,
  RegisterWebhookRequest,
  WebhookDelivery,
  FeeSchedule,
  FiatDepositRequest,
  FiatDepositResponse,
  FiatWithdrawRequest,
  FiatWithdrawResponse,
  BatchTransferRequest,
  BatchTransferResponse,
  ScheduleTransferRequest,
  ScheduleTransferResponse,
  APIError,
} from './types';

function getApiUrl(): string {
  return process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080';
}

function getAuthToken(): string | null {
  if (typeof window === 'undefined') return null;
  return localStorage.getItem('fluxa_api_key');
}

class ApiClientError extends Error {
  code: string;
  status: number;

  constructor(code: string, message: string, status: number) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const token = getAuthToken();
  const url = `${getApiUrl()}${path}`;

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };

  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const res = await fetch(url, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });

  if (!res.ok) {
    let errorData: APIError | null = null;
    try {
      errorData = await res.json();
    } catch {
      // ignore parse errors
    }
    throw new ApiClientError(
      errorData?.error?.code || 'UNKNOWN_ERROR',
      errorData?.error?.message || `Request failed with status ${res.status}`,
      res.status,
    );
  }

  if (res.status === 204) {
    return undefined as T;
  }

  return res.json();
}

export const api = {
  // Health
  health: () => request<{ status: string }>('GET', '/health'),

  // Wallets
  createWallet: () => request<Wallet>('POST', '/v1/wallets'),
  getWalletBalances: (id: string) =>
    request<WalletBalances>('GET', `/v1/wallets/${id}/balances`),

  // Transfers
  createTransfer: (data: CreateTransferRequest) =>
    request<Transaction>('POST', '/v1/transfers', data),
  getTransaction: (id: string) =>
    request<Transaction>('GET', `/v1/transfers/${id}`),
  listTransactions: (walletId: string, limit = 20, offset = 0) =>
    request<TransactionsResponse>(
      'GET',
      `/v1/transactions?wallet_id=${walletId}&limit=${limit}&offset=${offset}`,
    ),

  // FX / Conversions
  getFxQuote: (data: FxQuoteRequest) =>
    request<FxQuoteResponse>('POST', '/v1/fx/quote', data),
  executeConversion: (data: FxConvertRequest) =>
    request<Conversion>('POST', '/v1/fx/convert', data),
  getFxRates: (from: string, to: string) =>
    request<FxRatesResponse>('GET', `/v1/fx/rates?from=${from}&to=${to}`),

  // API Keys
  createApiKey: (label?: string) =>
    request<CreateAPIKeyResponse>('POST', '/v1/keys', { label }),
  listApiKeys: () => request<APIKey[]>('GET', '/v1/keys'),
  revokeApiKey: (id: string) =>
    request<void>('DELETE', `/v1/keys/${id}`),

  // Webhooks
  registerWebhook: (data: RegisterWebhookRequest) =>
    request<WebhookEndpoint>('POST', '/v1/webhooks', data),
  listWebhooks: () => request<WebhookEndpoint[]>('GET', '/v1/webhooks'),
  deleteWebhook: (id: string) =>
    request<void>('DELETE', `/v1/webhooks/${id}`),
  listWebhookDeliveries: (id: string, limit = 20, offset = 0) =>
    request<WebhookDelivery[]>(
      'GET',
      `/v1/webhooks/${id}/deliveries?limit=${limit}&offset=${offset}`,
    ),

  // Fees
  getFeeSchedule: () => request<FeeSchedule>('GET', '/v1/fees'),

  // Fiat
  depositFiat: (walletId: string, data: FiatDepositRequest) =>
    request<FiatDepositResponse>(
      'POST',
      `/v1/wallets/${walletId}/deposit/fiat`,
      data,
    ),
  withdrawFiat: (walletId: string, data: FiatWithdrawRequest) =>
    request<FiatWithdrawResponse>(
      'POST',
      `/v1/wallets/${walletId}/withdraw/fiat`,
      data,
    ),

  // Batch Transfers
  createBatchTransfer: (data: BatchTransferRequest) =>
    request<BatchTransferResponse>('POST', '/v1/transfers/batch', data),
  getBatchTransfer: (batchId: string) =>
    request<BatchTransferResponse>('GET', `/v1/transfers/batch/${batchId}`),

  // Scheduled Transfers
  createSchedule: (data: ScheduleTransferRequest) =>
    request<ScheduleTransferResponse>('POST', '/v1/schedules', data),
  listSchedules: () =>
    request<ScheduleTransferResponse[]>('GET', '/v1/schedules'),
  updateSchedule: (id: string, data: Partial<ScheduleTransferRequest> & { status?: string }) =>
    request<ScheduleTransferResponse>('PATCH', `/v1/schedules/${id}`, data),
  cancelSchedule: (id: string) =>
    request<void>('DELETE', `/v1/schedules/${id}`),
};

export { ApiClientError };
