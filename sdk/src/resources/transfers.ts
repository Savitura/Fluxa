import { HttpClient, RequestOptions } from '../http';
import { makeIdempotencyKey } from '../http';
import {
  CreateTransferRequest,
  TransferResponse,
  ListTransactionsQuery,
  ListTransactionsResponse,
  BatchResponse,
  ListBatchesQuery,
  ListBatchesResponse,
  BatchPreflightResponse,
} from '../types';
import { Page, createPage, paginate, paginateAll } from '../pagination';

export class TransfersResource {
  constructor(private http: HttpClient) {}

  async create(
    request: CreateTransferRequest,
    options?: RequestOptions,
  ): Promise<TransferResponse> {
    const res = await this.http.request<TransferResponse>({
      method: 'POST',
      path: '/transfers',
      body: request,
      signal: options?.signal,
      idempotencyKey: options?.idempotencyKey ?? makeIdempotencyKey(),
    });
    return res.data;
  }

  async get(transferId: string, options?: RequestOptions): Promise<TransferResponse> {
    const res = await this.http.request<TransferResponse>({
      method: 'GET',
      path: `/transfers/${encodeURIComponent(transferId)}`,
      signal: options?.signal,
    });
    return res.data;
  }

  async list(
    query: ListTransactionsQuery,
    options?: RequestOptions,
  ): Promise<ListTransactionsResponse> {
    const res = await this.http.request<ListTransactionsResponse>({
      method: 'GET',
      path: '/transfers',
      query,
      signal: options?.signal,
    });
    return res.data;
  }

  /**
   * Fetches a single page of transfers formatted as a standard Page<TransferResponse>.
   */
  async listPage(
    query: ListTransactionsQuery,
    options?: RequestOptions,
  ): Promise<Page<TransferResponse>> {
    const res = await this.list(query, options);
    const nextCursor = res.next_cursor ?? res.cursor ?? null;
    return createPage(res.transactions ?? [], nextCursor);
  }

  /**
   * Returns an async iterator that iterates over individual transfers across pages.
   * Supports `for await (const transfer of client.transfers.iterate(query))`.
   */
  iterate(
    query: ListTransactionsQuery,
    options?: RequestOptions,
  ): AsyncIterableIterator<TransferResponse> {
    return paginate<TransferResponse, ListTransactionsQuery>({
      fetchPage: (q, opts) => this.listPage(q, opts),
      query,
      options,
    });
  }

  /**
   * Fetches all transfers across all pages into a consolidated array.
   */
  async listAll(
    query: ListTransactionsQuery,
    options?: RequestOptions,
  ): Promise<TransferResponse[]> {
    return paginateAll<TransferResponse, ListTransactionsQuery>({
      fetchPage: (q, opts) => this.listPage(q, opts),
      query,
      options,
    });
  }

  async createBatch(
    request: import('../types').CreateBatchRequest,
    options?: RequestOptions,
  ): Promise<BatchResponse> {
    const res = await this.http.request<BatchResponse>({
      method: 'POST',
      path: '/transfers/batch',
      body: request,
      signal: options?.signal,
      idempotencyKey: options?.idempotencyKey ?? makeIdempotencyKey(),
    });
    return res.data;
  }

  async getBatch(batchId: string, options?: RequestOptions): Promise<BatchResponse> {
    const res = await this.http.request<BatchResponse>({
      method: 'GET',
      path: `/transfers/batch/${encodeURIComponent(batchId)}`,
      signal: options?.signal,
    });
    return res.data;
  }

  async exportBatch(batchId: string, options?: RequestOptions): Promise<string> {
    const res = await this.http.request<string>({
      method: 'GET',
      path: `/transfers/batch/${encodeURIComponent(batchId)}/export`,
      signal: options?.signal,
    });
    return res.data;
  }

  /**
   * Lists batch transfer history for the authenticated tenant.
   * Supports cursor pagination, status filtering, and source-wallet filtering.
   */
  async listBatches(
    query: ListBatchesQuery = {},
    options?: RequestOptions,
  ): Promise<ListBatchesResponse> {
    const res = await this.http.request<ListBatchesResponse>({
      method: 'GET',
      path: '/transfers/batches',
      query: query as Record<string, unknown>,
      signal: options?.signal,
    });
    return res.data;
  }

  /**
   * Validates a batch transfer request without submitting anything.
   * Returns per-row results with estimated fees and net amounts.
   * The request shape is identical to createBatch.
   */
  async validateBatch(
    request: import('../types').CreateBatchRequest,
    options?: RequestOptions,
  ): Promise<BatchPreflightResponse> {
    const res = await this.http.request<BatchPreflightResponse>({
      method: 'POST',
      path: '/transfers/batch/validate',
      body: request,
      signal: options?.signal,
    });
    return res.data;
  }
}
