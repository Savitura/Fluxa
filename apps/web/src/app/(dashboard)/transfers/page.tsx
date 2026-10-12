'use client';

import { useEffect, useState, useCallback, useMemo } from 'react';
import { api, type Transaction } from '@/lib/api';
import { useAuth } from '@/lib/auth-context';
import { useToast } from '@/lib/toast-context';
import { PageHeader } from '@/components/ui/page-header';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Badge } from '@/components/ui/badge';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { EmptyState } from '@/components/ui/empty-state';
import { Skeleton } from '@/components/ui/skeleton';
import { ArrowRightLeft, ExternalLink, Plus, RotateCcw, X } from 'lucide-react';

function statusBadge(status: string) {
  if (status === 'confirmed') return <Badge variant="success">{status}</Badge>;
  if (status === 'pending') return <Badge variant="warning">{status}</Badge>;
  return <Badge variant="danger">{status}</Badge>;
}

export default function TransfersPage() {
  const { getStoredWalletIds } = useAuth();
  const { toast } = useToast();
  const [transfers, setTransfers] = useState<Transaction[]>([]);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState('all');
  const [showForm, setShowForm] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [refundTarget, setRefundTarget] = useState<Transaction | null>(null);
  const [refundAmount, setRefundAmount] = useState('');
  const [refundReason, setRefundReason] = useState('');
  const [refundBusy, setRefundBusy] = useState(false);
  const [refunds, setRefunds] = useState<Record<string, { amount: string; status: string }[]>>({});
  const [form, setForm] = useState({
    from_wallet_id: '',
    to_wallet_id: '',
    asset: 'XLM',
    amount: '',
  });

  const walletIds = useMemo(() => getStoredWalletIds(), [getStoredWalletIds]);

  const fetchTransfers = useCallback(async () => {
    setLoading(true);
    try {
      const allTx: Transaction[] = [];
      for (const id of walletIds) {
        try {
          const res = await api.listTransactions(id, 50);
          allTx.push(...(res.transactions || []));
        } catch {}
      }
      const unique = Array.from(new Map(allTx.map((t) => [t.id, t])).values());
      unique.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime());
      setTransfers(unique);
    } catch {
      toast('Failed to load transfers', 'error');
    } finally {
      setLoading(false);
    }
  }, [walletIds, toast]);

  useEffect(() => {
    let cancelled = false;
    const run = async () => {
      if (cancelled) return;
      await fetchTransfers();
    };
    run();
    return () => {
      cancelled = true;
    };
  }, [fetchTransfers]);

  const handleCreateTransfer = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      await api.createTransfer(form);
      toast('Transfer initiated', 'success');
      setShowForm(false);
      setForm({
        from_wallet_id: '',
        to_wallet_id: '',
        asset: 'XLM',
        amount: '',
      });
      await fetchTransfers();
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Transfer failed', 'error');
    } finally {
      setSubmitting(false);
    }
  };

  const submitRefund = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!refundTarget) return;
    setRefundBusy(true);
    try {
      const refund = await api.createRefund({
        original_transaction_id: refundTarget.id,
        amount: refundAmount,
        reason: refundReason,
      });
      setRefunds((current) => ({
        ...current,
        [refundTarget.id]: [...(current[refundTarget.id] || []), refund],
      }));
      setRefundTarget(null);
      setRefundAmount('');
      setRefundReason('');
      toast(`Refund ${refund.status}`, 'success');
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Refund failed', 'error');
    } finally {
      setRefundBusy(false);
    }
  };

  const loadRefunds = async (transaction: Transaction) => {
    if (refunds[transaction.id]) return;
    try {
      const result = await api.listRefunds(transaction.id);
      setRefunds((current) => ({ ...current, [transaction.id]: result.refunds }));
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Could not load refunds', 'error');
    }
  };

  const filtered = filter === 'all' ? transfers : transfers.filter((t) => t.status === filter);

  if (loading) {
    return (
      <div className="flex flex-col gap-8">
        <div className="flex items-center justify-between">
          <Skeleton className="h-10 w-48" />
          <Skeleton className="h-10 w-32" />
        </div>
        <Skeleton className="h-64" />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-8 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <PageHeader title="Transfers" description="View and trace your transfer history.">
        <div className="flex items-center gap-3">
          <Select value={filter} onChange={(e) => setFilter(e.target.value)} className="w-40">
            <option value="all">All Statuses</option>
            <option value="confirmed">Confirmed</option>
            <option value="pending">Pending</option>
            <option value="failed">Failed</option>
          </Select>
          {walletIds.length >= 2 && (
            <Button
              variant={showForm ? 'secondary' : 'primary'}
              onClick={() => setShowForm(!showForm)}
            >
              {showForm ? (
                <>
                  <X className="h-4 w-4" /> Cancel
                </>
              ) : (
                <>
                  <Plus className="h-4 w-4" /> New Transfer
                </>
              )}
            </Button>
          )}
        </div>
      </PageHeader>

      {showForm && (
        <Card className="max-w-2xl">
          <CardHeader>
            <CardTitle>New Transfer</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleCreateTransfer} className="flex flex-col gap-5">
              <div className="grid grid-cols-1 gap-5 md:grid-cols-2">
                <div className="flex flex-col gap-1.5">
                  <label className="text-sm font-medium text-foreground">From Wallet</label>
                  <Select
                    value={form.from_wallet_id}
                    onChange={(e) => setForm({ ...form, from_wallet_id: e.target.value })}
                    required
                  >
                    <option value="">Select wallet</option>
                    {walletIds.map((id) => (
                      <option key={id} value={id}>
                        {id.slice(0, 16)}...
                      </option>
                    ))}
                  </Select>
                </div>
                <div className="flex flex-col gap-1.5">
                  <label className="text-sm font-medium text-foreground">To Wallet</label>
                  <Select
                    value={form.to_wallet_id}
                    onChange={(e) => setForm({ ...form, to_wallet_id: e.target.value })}
                    required
                  >
                    <option value="">Select wallet</option>
                    {walletIds.map((id) => (
                      <option key={id} value={id}>
                        {id.slice(0, 16)}...
                      </option>
                    ))}
                  </Select>
                </div>
              </div>
              <div className="grid grid-cols-1 gap-5 md:grid-cols-2">
                <div className="flex flex-col gap-1.5">
                  <label className="text-sm font-medium text-foreground">Asset</label>
                  <Input
                    value={form.asset}
                    onChange={(e) => setForm({ ...form, asset: e.target.value })}
                    required
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <label className="text-sm font-medium text-foreground">Amount</label>
                  <Input
                    value={form.amount}
                    onChange={(e) => setForm({ ...form, amount: e.target.value })}
                    required
                    placeholder="0.0000000"
                    className="font-mono"
                  />
                </div>
              </div>
              <div className="flex justify-end">
                <Button
                  type="submit"
                  isLoading={submitting}
                  disabled={!form.from_wallet_id || !form.to_wallet_id || !form.amount}
                >
                  Initiate Transfer
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      )}

      <Card>
        {filtered.length === 0 ? (
          <EmptyState
            icon={ArrowRightLeft}
            title="No transfers found"
            description="Create wallets and initiate a transfer to get started."
          />
        ) : (
          <Table>
            <TableHead>
              <TableRow>
                <TableHeader>ID</TableHeader>
                <TableHeader>Amount</TableHeader>
                <TableHeader>From / To</TableHeader>
                <TableHeader>Date</TableHeader>
                <TableHeader>Status</TableHeader>
                <TableHeader>Failure</TableHeader>
                <TableHeader>Stellar Tx</TableHeader>
                <TableHeader>Refunds</TableHeader>
              </TableRow>
            </TableHead>
            <TableBody>
              {filtered.map((tr) => (
                <TableRow key={tr.id}>
                  <TableCell className="font-mono text-muted-foreground">
                    {tr.id.slice(0, 8)}
                  </TableCell>
                  <TableCell className="font-medium">
                    {tr.amount} {tr.asset}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {tr.from_wallet_id.slice(0, 8)}... &rarr; {tr.to_wallet_id.slice(0, 8)}...
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {new Date(tr.created_at).toLocaleString()}
                  </TableCell>
                  <TableCell>{statusBadge(tr.status)}</TableCell>
                  <TableCell>{tr.failure_message || tr.failure_reason || '—'}</TableCell>
                  <TableCell>
                    {tr.tx_hash ? (
                      <a
                        href={`https://stellar.expert/explorer/public/tx/${tr.tx_hash}`}
                        target="_blank"
                        rel="noreferrer"
                        className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:text-primary-hover hover:underline"
                      >
                        {tr.tx_hash.slice(0, 8)}...
                        <ExternalLink className="h-3.5 w-3.5" />
                      </a>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-col items-start gap-1">
                      {refunds[tr.id]?.map((refund, index) => (
                        <span key={`${tr.id}-${index}`} className="text-xs text-muted-foreground">
                          {refund.amount} · {refund.status}
                        </span>
                      ))}
                      {(tr.status === 'confirmed' || tr.status === 'settled') &&
                        tr.type === 'transfer' && (
                          <div className="flex gap-1">
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              onClick={() => {
                                setRefundTarget(tr);
                                setRefundAmount('');
                              }}
                            >
                              <RotateCcw className="h-3.5 w-3.5" />
                              Refund
                            </Button>
                            {!refunds[tr.id] && (
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                onClick={() => void loadRefunds(tr)}
                              >
                                History
                              </Button>
                            )}
                          </div>
                        )}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>
      {refundTarget && (
        <div
          className="fixed inset-0 z-50 grid place-items-center bg-black/40 p-4"
          role="presentation"
          onMouseDown={(event) => {
            if (event.target === event.currentTarget) setRefundTarget(null);
          }}
        >
          <section
            role="dialog"
            aria-modal="true"
            aria-labelledby="refund-title"
            className="w-full max-w-md border border-border bg-surface p-6 shadow-xl"
          >
            <div className="mb-5 flex items-center justify-between">
              <h2 id="refund-title" className="text-lg font-semibold">
                Refund transfer
              </h2>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label="Close refund form"
                onClick={() => setRefundTarget(null)}
              >
                <X className="h-4 w-4" />
              </Button>
            </div>
            <p className="mb-5 text-sm text-muted-foreground">
              Original transaction {refundTarget.id.slice(0, 8)} · up to {refundTarget.net_amount}{' '}
              {refundTarget.asset}
            </p>
            <form onSubmit={submitRefund} className="flex flex-col gap-4">
              <label className="flex flex-col gap-1.5 text-sm font-medium">
                Amount
                <Input
                  type="number"
                  min="0.0000001"
                  step="0.0000001"
                  max={refundTarget.net_amount}
                  value={refundAmount}
                  onChange={(event) => setRefundAmount(event.target.value)}
                  required
                />
              </label>
              <label className="flex flex-col gap-1.5 text-sm font-medium">
                Reason
                <Input
                  value={refundReason}
                  onChange={(event) => setRefundReason(event.target.value)}
                  maxLength={500}
                />
              </label>
              <div className="flex justify-end gap-2">
                <Button type="button" variant="secondary" onClick={() => setRefundTarget(null)}>
                  Cancel
                </Button>
                <Button type="submit" isLoading={refundBusy}>
                  Issue refund
                </Button>
              </div>
            </form>
          </section>
        </div>
      )}
    </div>
  );
}
