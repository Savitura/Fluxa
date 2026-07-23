'use client';

import { useState } from 'react';
import { api, ApiClientError } from '@/lib/api';
import { Transaction } from '@/lib/types';

export default function TransfersPage() {
  const [transactions, setTransactions] = useState<Transaction[]>([]);
  const [walletId, setWalletId] = useState(() => {
    if (typeof window === 'undefined') return '';
    const stored = localStorage.getItem('fluxa_wallets');
    if (stored) {
      const wallets = JSON.parse(stored);
      if (wallets.length > 0) return wallets[0].id;
    }
    return '';
  });
  const [filter, setFilter] = useState('All');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const [fromWalletId, setFromWalletId] = useState('');
  const [toWalletId, setToWalletId] = useState('');
  const [asset, setAsset] = useState('USDC');
  const [amount, setAmount] = useState('');
  const [isCreating, setIsCreating] = useState(false);

  const fetchTransactions = async () => {
    if (!walletId) return;
    setIsLoading(true);
    setError('');

    try {
      const data = await api.listTransactions(walletId);
      setTransactions(data.transactions);
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.message);
      } else {
        setError('Failed to load transactions');
      }
    } finally {
      setIsLoading(false);
    }
  };

  const handleCreateTransfer = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsCreating(true);
    setError('');

    try {
      await api.createTransfer({
        from_wallet_id: fromWalletId,
        to_wallet_id: toWalletId,
        asset,
        amount,
      });
      setFromWalletId('');
      setToWalletId('');
      setAmount('');
      if (walletId) fetchTransactions();
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.message);
      } else {
        setError('Transfer failed');
      }
    } finally {
      setIsCreating(false);
    }
  };

  const filteredTxs = filter === 'All'
    ? transactions
    : transactions.filter((t) => t.status.toLowerCase() === filter.toLowerCase());

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

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex justify-between items-center gap-4">
        <div>
          <h1 className="text-[2rem] font-bold tracking-tight">Transfers</h1>
          <p className="text-muted text-[1.05rem] mt-1">View, trace, and initiate transfers.</p>
        </div>
      </header>

      <div className="glass p-8 flex flex-col gap-6 max-w-[600px] rounded-2xl">
        <h3 className="text-xl font-semibold m-0">New Transfer</h3>
        <form onSubmit={handleCreateTransfer} className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <label className="text-sm font-medium text-muted">From Wallet ID</label>
            <input
              type="text"
              value={fromWalletId}
              onChange={(e) => setFromWalletId(e.target.value)}
              required
              placeholder="uuid"
              className="bg-black/20 border border-border px-4 py-3 rounded-lg text-foreground text-base outline-none focus:border-accent font-mono"
            />
          </div>
          <div className="flex flex-col gap-2">
            <label className="text-sm font-medium text-muted">To Wallet ID</label>
            <input
              type="text"
              value={toWalletId}
              onChange={(e) => setToWalletId(e.target.value)}
              required
              placeholder="uuid"
              className="bg-black/20 border border-border px-4 py-3 rounded-lg text-foreground text-base outline-none focus:border-accent font-mono"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium text-muted">Asset</label>
              <select
                value={asset}
                onChange={(e) => setAsset(e.target.value)}
                className="bg-black/20 border border-border px-4 py-3 rounded-lg text-foreground text-base outline-none focus:border-accent"
              >
                <option value="USDC" className="bg-zinc-900">USDC</option>
                <option value="XLM" className="bg-zinc-900">XLM</option>
                <option value="EURC" className="bg-zinc-900">EURC</option>
              </select>
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium text-muted">Amount</label>
              <input
                type="text"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                required
                placeholder="100.00"
                className="bg-black/20 border border-border px-4 py-3 rounded-lg text-foreground text-base outline-none focus:border-accent"
              />
            </div>
          </div>
          {error && <p className="text-red-500 text-sm">{error}</p>}
          <button
            type="submit"
            disabled={isCreating}
            className="bg-accent hover:bg-accent-hover text-white px-6 py-3 rounded-lg font-semibold transition-all hover:-translate-y-px disabled:opacity-70 disabled:cursor-not-allowed"
          >
            {isCreating ? 'Initiating...' : 'Initiate Transfer'}
          </button>
        </form>
      </div>

      <div className="flex flex-col gap-4">
        <div className="flex justify-between items-center">
          <h2 className="text-xl font-semibold m-0">Transaction History</h2>
          <div className="flex gap-4 items-center">
            <div className="flex gap-2">
              <input
                type="text"
                value={walletId}
                onChange={(e) => setWalletId(e.target.value)}
                placeholder="Wallet ID"
                className="bg-black/20 border border-border px-3 py-2 rounded-lg text-foreground text-sm outline-none focus:border-accent font-mono w-64"
              />
              <button
                onClick={fetchTransactions}
                disabled={!walletId || isLoading}
                className="bg-accent hover:bg-accent-hover text-white px-4 py-2 rounded-lg font-medium transition-all disabled:opacity-70 disabled:cursor-not-allowed cursor-pointer"
              >
                {isLoading ? 'Loading...' : 'Fetch'}
              </button>
            </div>
            <select
              className="bg-black/20 border border-border px-4 py-2.5 rounded-lg text-foreground text-[15px] cursor-pointer outline-none focus:border-accent"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            >
              <option value="All" className="bg-zinc-900 text-foreground">All Statuses</option>
              <option value="Confirmed" className="bg-zinc-900 text-foreground">Confirmed</option>
              <option value="Pending" className="bg-zinc-900 text-foreground">Pending</option>
              <option value="Failed" className="bg-zinc-900 text-foreground">Failed</option>
            </select>
          </div>
        </div>

        {filteredTxs.length === 0 ? (
          <div className="glass p-12 flex flex-col items-center gap-4 rounded-2xl text-center">
            <p className="text-4xl">💸</p>
            <h3 className="text-xl font-semibold">No Transactions</h3>
            <p className="text-muted max-w-md">Enter a wallet ID and click Fetch to view transactions, or initiate a transfer above.</p>
          </div>
        ) : (
          <div className="glass rounded-xl overflow-hidden">
            <table className="w-full text-left border-collapse">
              <thead>
                <tr>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">ID</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Type</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Amount</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">From / To</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Date</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Status</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Hash</th>
                </tr>
              </thead>
              <tbody>
                {filteredTxs.map((tx) => (
                  <tr key={tx.id} className="transition-colors hover:bg-white/5 border-b border-border last:border-0">
                    <td className="p-5 text-muted text-sm font-mono">{tx.id.slice(0, 8)}...</td>
                    <td className="p-5 text-sm capitalize">{tx.type}</td>
                    <td className="p-5 font-medium">{tx.amount} {tx.asset}</td>
                    <td className="p-5 text-muted text-sm font-mono">
                      {tx.from_wallet_id.slice(0, 8)}... &rarr; {tx.to_wallet_id.slice(0, 8)}...
                    </td>
                    <td className="p-5 text-muted text-sm">{new Date(tx.created_at).toLocaleDateString()}</td>
                    <td className="p-5">
                      <span className={`inline-block px-3 py-1 rounded-full text-[11px] font-bold uppercase tracking-wider ${statusColor(tx.status)}`}>
                        {tx.status}
                      </span>
                    </td>
                    <td className="p-5">
                      {tx.tx_hash ? (
                        <a
                          href={`https://stellar.expert/explorer/public/tx/${tx.tx_hash}`}
                          target="_blank"
                          rel="noreferrer"
                          className="text-accent font-medium text-sm hover:text-accent-hover hover:underline"
                        >
                          {tx.tx_hash.slice(0, 8)}... ↗
                        </a>
                      ) : (
                        <span className="text-muted">—</span>
                      )}
                    </td>
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
