import { classifyError, FluxaError, RateLimitError } from './errors';

export interface HttpClientConfig {
  baseUrl: string;
  apiKey: string;
  timeout: number;
  maxRetries: number;
  retryDelay: number;
  idempotencyKey?: string;
  requireIdempotencyKey?: boolean;
}

export interface RequestOptions {
  signal?: AbortSignal;
  idempotencyKey?: string;
}

export interface HttpRequestOptions extends RequestOptions {
  method: string;
  path: string;
  body?: unknown;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  query?: Record<string, any>;
  headers?: Record<string, string>;
  timeout?: number;
  /**
   * Opt in to retrying a mutation that the server does not support
   * idempotency for. When false (default), a mutation without an
   * idempotency key will fail fast instead of being silently replayed.
   */
  allowUnsafeRetry?: boolean;
}

export interface HttpResponse<T> {
  data: T;
  status: number;
  headers: Headers;
}

function buildQueryString(
  params?:
    Record<string, string | number | undefined> | { [key: string]: string | number | undefined },
): string {
  if (!params) return '';
  const entries = Object.entries(params).filter(([, v]) => v !== undefined && v !== null);
  if (entries.length === 0) return '';
  const qs = new URLSearchParams();
  for (const [k, v] of entries) {
    qs.set(k, String(v));
  }
  return `?${qs.toString()}`;
}

function isRetryable(status: number): boolean {
  return status === 429 || status >= 500;
}

function requiresIdempotencyKey(method: string, path: string): boolean {
  if (method.toUpperCase() !== 'POST') return false;
  if (
    [
      '/wallets',
      '/transfers',
      '/transfers/batch',
      '/withdrawals',
      '/fx/convert',
      '/schedules',
      '/claimable-balances',
      '/payment-links',
      '/refunds',
      '/webhooks/secret/rotate',
    ].includes(path)
  ) {
    return true;
  }
  return (
    (path.startsWith('/wallets/') &&
      ['/deposit/fiat', '/withdraw/fiat', '/trustlines'].some((suffix) => path.endsWith(suffix))) ||
    (path.startsWith('/claimable-balances/') && path.endsWith('/claim'))
  );
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) return seconds * 1000;
  const date = Date.parse(value);
  return Number.isFinite(date) ? Math.max(0, date - Date.now()) : undefined;
}

export function makeIdempotencyKey(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') {
    return globalThis.crypto.randomUUID();
  }
  // RFC4122 v4 fallback for environments without web crypto.
  const bytes = new Uint8Array(16);
  if (typeof globalThis.crypto?.getRandomValues === 'function') {
    globalThis.crypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < bytes.length; i++) {
      bytes[i] = Math.floor(Math.random() * 256);
    }
  }
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export class HttpClient {
  private config: HttpClientConfig;

  constructor(config: HttpClientConfig) {
    this.config = config;
  }

  async request<T>(options: HttpRequestOptions): Promise<HttpResponse<T>> {
    const { method, path, body, query, headers: extraHeaders, signal } = options;
    const rootUrl = this.config.baseUrl.replace(/\/$/, '').replace(/\/v1$/, '');
    const url = `${rootUrl}${path === '/../health' ? '/health' : `/v1${path}`}${buildQueryString(query)}`;

    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${this.config.apiKey}`,
      ...extraHeaders,
    };
    const existingKey = Object.entries(headers).find(([name]) =>
      ['idempotency-key', 'x-idempotency-key'].includes(name.toLowerCase()),
    )?.[1];
    const mutating = !['GET', 'HEAD'].includes(method.toUpperCase());
    const needsKey = requiresIdempotencyKey(method, path);
    const idempotencyKey =
      options.idempotencyKey ||
      existingKey ||
      this.config.idempotencyKey ||
      (needsKey ? makeIdempotencyKey() : undefined);
    if (idempotencyKey && !existingKey) headers['Idempotency-Key'] = idempotencyKey;

    // Mutations are only retryable when they carry an idempotency key.
    // Otherwise a lost response could duplicate a financial operation.
    const safeToRetry = !mutating || Boolean(idempotencyKey) || Boolean(options.allowUnsafeRetry);

    let lastError: Error | undefined;
    let nextRetryDelay: number | undefined;

    for (let attempt = 0; attempt <= this.config.maxRetries; attempt++) {
      if (attempt > 0) {
        const delay = nextRetryDelay ?? this.config.retryDelay * Math.pow(2, attempt - 1);
        nextRetryDelay = undefined;
        await sleep(delay);
      }

      const controller = new AbortController();
      const requestTimeout = options.timeout ?? this.config.timeout;
      const timeoutId = setTimeout(() => controller.abort(), requestTimeout);

      // Combine external signal with timeout signal
      if (signal) {
        if (signal.aborted) {
          controller.abort();
        } else {
          signal.addEventListener('abort', () => controller.abort(), {
            once: true,
          });
        }
      }

      try {
        const res = await fetch(url, {
          method,
          headers,
          body: body ? JSON.stringify(body) : undefined,
          signal: controller.signal,
        });

        clearTimeout(timeoutId);

        // Handle empty responses (204 No Content)
        if (res.status === 204) {
          return { data: undefined as unknown as T, status: res.status, headers: res.headers };
        }

        const contentType = res.headers.get('content-type') ?? '';
        let responseData: unknown;

        if (contentType.includes('application/json')) {
          responseData = await res.json();
        } else if (contentType.includes('text/csv')) {
          responseData = await res.text();
        } else {
          responseData = await res.text();
        }

        if (!res.ok) {
          const error = classifyError(res.status, responseData);

          if (error instanceof RateLimitError) {
            const retryHeader = res.headers.get('retry-after');
            if (retryHeader) {
              const retryAfterMs = parseRetryAfter(retryHeader);
              if (retryAfterMs !== undefined) error.retryAfter = Math.ceil(retryAfterMs / 1000);
            }
          }

          if (safeToRetry && isRetryable(res.status) && attempt < this.config.maxRetries) {
            lastError = error;
            nextRetryDelay = parseRetryAfter(res.headers.get('retry-after'));
            continue;
          }

          throw error;
        }

        return { data: responseData as T, status: res.status, headers: res.headers };
      } catch (err) {
        clearTimeout(timeoutId);

        if (err instanceof FluxaError) {
          throw err;
        }

        lastError = err instanceof Error ? err : new Error(String(err));
        // Network or abort errors are retryable
        if (safeToRetry && attempt < this.config.maxRetries) {
          continue;
        }

        if (err instanceof DOMException && err.name === 'AbortError') {
          throw new FluxaError(408, {
            code: 'TIMEOUT',
            message: `Request timed out after ${options.timeout ?? this.config.timeout}ms`,
          });
        }

        throw new FluxaError(0, {
          code: 'NETWORK_ERROR',
          message: lastError?.message ?? 'Network request failed',
        });
      }
    }

    throw lastError ?? new FluxaError(0, { code: 'UNKNOWN', message: 'Request failed' });
  }
}
