'use client';

import { useState, useEffect } from 'react';
import { api, ApiClientError } from '@/lib/api';
import { Transaction, FeeSchedule, Balance } from '@/lib/types';

interface StoredWallet {
  id: string;
  public_key: string;
  created_at: string;
  balances: Balance[];
}

export default function OverviewPage() {
  const [wallets] = useState<StoredWallet[]>(() => {
    if (typeof window === 'undefined') return [];
    const stored = localStorage.getItem('fluxa_wallets');
    return stored ? JSON.parse(stored) : [];
  });
  const [transactions, setTransactions] = useState<Transaction[]>([]);
  const [feeSchedule, setFeeSchedule] = useState<FeeSchedule | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let mounted = true;

    const load = async () => {
      try {
        const feeData = await api.getFeeSchedule().catch(() => null);
        if (mounted) setFeeSchedule(feeData);

        if (wallets.length > 0) {
          const txData = await api.listTransactions(wallets[0].id, 5);
          if (mounted) setTransactions(txData.transactions);
        }
      } catch (err) {
        if (mounted && err instanceof ApiClientError) {
          setError(err.message);
        }
      } finally {
        if (mounted) setIsLoading(false);
      }
    };

    load();

    return () => { mounted = false; };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const totalBalance = wallets.reduce((sum, w) => {
    const balances = w.balances || [];
    return sum + balances.reduce((s, b) => s + parseFloat(b.balance || '0'), 0);
  }, 0);

  const statusColor = (status: string) => {
    switch (status.toLowerCase()) {
      case 'confirmed':
        return 'bg-emerald-500/15 text-emerald-500';
      case 'pending':
      case 'submitted':
        return 'bg-amber-500/15 text-amber-500';
      case 'failed':
      case 'reconciliation_failed':
        return 'bg-red-500/15 text-red-500';
      default:
        return 'bg-zinc-500/15 text-zinc-400';
    }
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="text-muted text-lg">Loading dashboard...</div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex flex-col gap-2">
        <h1 className="text-[2rem] font-bold tracking-tight">Dashboard Overview</h1>
        <p className="text-muted text-[1.05rem]">Welcome back. Here&apos;s what&apos;s happening today.</p>
      </header>

      {error && (
        <div className="bg-red-500/10 border border-red-500/30 text-red-500 p-4 rounded-lg text-sm">
          {error}
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        <div className="glass p-6 flex flex-col gap-3 rounded-xl transition-all hover:-translate-y-1 hover:shadow-2xl">
          <h3 className="text-sm font-medium text-muted uppercase tracking-wider">Total Balance</h3>
          <p className="text-4xl font-bold tracking-tight">
            {totalBalance > 0 ? totalBalance.toFixed(2) : '0.00'}
          </p>
          <p className="text-sm text-muted">{wallets.length} wallet{wallets.length !== 1 ? 's' : ''}</p>
        </div>
        <div className="glass p-6 flex flex-col gap-3 rounded-xl transition-all hover:-translate-y-1 hover:shadow-2xl">
          <h3 className="text-sm font-medium text-muted uppercase tracking-wider">Transfer Fee</h3>
          <p className="text-4xl font-bold tracking-tight">
            {feeSchedule ? `${feeSchedule.transfer_fee_bps} bps` : '—'}
          </p>
          <p className="text-sm text-muted">
            {feeSchedule ? `Min: ${feeSchedule.min_fee_amount} ${feeSchedule.asset}` : 'No fee data'}
          </p>
        </div>
        <div className="glass p-6 flex flex-col gap-3 rounded-xl transition-all hover:-translate-y-1 hover:shadow-2xl">
          <h3 className="text-sm font-medium text-muted uppercase tracking-wider">Conversion Fee</h3>
          <p className="text-4xl font-bold tracking-tight">
            {feeSchedule ? `${feeSchedule.conversion_fee_bps} bps` : '—'}
          </p>
          <p className="text-sm text-muted">
            {feeSchedule ? `Asset: ${feeSchedule.asset}` : 'No fee data'}
          </p>
        </div>
      </div>

      <div className="flex flex-col gap-4">
        <h2 className="text-xl font-semibold">Recent Transactions</h2>
        {transactions.length === 0 ? (
          <div className="glass p-8 rounded-xl text-center text-muted">
            {wallets.length > 0
              ? 'No recent transactions for this wallet.'
              : 'Create a wallet to start transacting.'}
          </div>
        ) : (
          <div className="glass rounded-xl overflow-hidden">
            <table className="w-full text-left border-collapse">
              <thead>
                <tr>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">ID</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Type</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Amount</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Status</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Date</th>
                </tr>
              </thead>
              <tbody>
                {transactions.map((tx) => (
                  <tr key={tx.id} className="transition-colors hover:bg-white/5 border-b border-border last:border-0">
                    <td className="p-5 text-muted text-sm font-mono">{tx.id.slice(0, 8)}...</td>
                    <td className="p-5 text-sm capitalize">{tx.type}</td>
                    <td className="p-5 font-medium">{tx.amount} {tx.asset}</td>
                    <td className="p-5">
                      <span className={`inline-block px-3 py-1 rounded-full text-[11px] font-bold uppercase tracking-wider ${statusColor(tx.status)}`}>
                        {tx.status}
                      </span>
                    </td>
                    <td className="p-5 text-muted text-sm">{new Date(tx.created_at).toLocaleDateString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
