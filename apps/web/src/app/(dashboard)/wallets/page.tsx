'use client';

import { useState, useCallback } from 'react';
import { api, ApiClientError } from '@/lib/api';
import { Wallet, Balance } from '@/lib/types';

interface StoredWallet extends Wallet {
  balances: Balance[];
}

function loadStoredWallets(): StoredWallet[] {
  if (typeof window === 'undefined') return [];
  const raw = localStorage.getItem('fluxa_wallets');
  return raw ? JSON.parse(raw) : [];
}

function saveStoredWallets(wallets: StoredWallet[]) {
  localStorage.setItem('fluxa_wallets', JSON.stringify(wallets));
}

export default function WalletsPage() {
  const [wallets, setWallets] = useState<StoredWallet[]>(loadStoredWallets);
  const [isCreating, setIsCreating] = useState(false);
  const [error, setError] = useState('');

  const refreshBalances = useCallback(async () => {
    const stored = loadStoredWallets();
    const updated = await Promise.all(
      stored.map(async (w) => {
        try {
          const data = await api.getWalletBalances(w.id);
          return { ...w, balances: data.balances };
        } catch {
          return w;
        }
      }),
    );
    setWallets(updated);
    saveStoredWallets(updated);
  }, []);

  const handleCreateWallet = async () => {
    setIsCreating(true);
    setError('');

    try {
      const wallet = await api.createWallet();
      const balancesData = await api.getWalletBalances(wallet.id);
      const stored: StoredWallet = {
        ...wallet,
        balances: balancesData.balances,
      };
      const updated = [...wallets, stored];
      setWallets(updated);
      saveStoredWallets(updated);
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.message);
      } else {
        setError('Failed to create wallet');
      }
    } finally {
      setIsCreating(false);
    }
  };

  const formatBalance = (balances: Balance[]) => {
    if (balances.length === 0) return '0.00';
    return balances.map((b) => `${b.balance} ${b.asset}`).join(', ');
  };

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex justify-between items-center gap-4">
        <div>
          <h1 className="text-[2rem] font-bold tracking-tight">Wallets</h1>
          <p className="text-muted text-[1.05rem] mt-1">Manage your Stellar wallets and balances.</p>
        </div>
        <div className="flex gap-3">
          {wallets.length > 0 && (
            <button
              onClick={refreshBalances}
              className="bg-transparent border border-border text-foreground px-4 py-3 rounded-lg font-medium hover:bg-white/5 transition-colors cursor-pointer"
            >
              Refresh Balances
            </button>
          )}
          <button
            className="bg-accent hover:bg-accent-hover text-white px-6 py-3 rounded-lg font-semibold shadow-[0_4px_12px_rgba(139,92,246,0.3)] transition-all hover:-translate-y-px disabled:opacity-70 disabled:cursor-not-allowed cursor-pointer"
            onClick={handleCreateWallet}
            disabled={isCreating}
          >
            {isCreating ? 'Creating...' : '+ Create Wallet'}
          </button>
        </div>
      </header>

      {error && (
        <div className="bg-red-500/10 border border-red-500/30 text-red-500 p-4 rounded-lg text-sm">
          {error}
        </div>
      )}

      {wallets.length === 0 ? (
        <div className="glass p-12 flex flex-col items-center gap-4 rounded-2xl text-center">
          <p className="text-4xl">💼</p>
          <h3 className="text-xl font-semibold">No Wallets Yet</h3>
          <p className="text-muted max-w-md">Create your first wallet to start managing balances on Stellar.</p>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6">
          {wallets.map((wallet) => (
            <div key={wallet.id} className="glass p-6 flex flex-col gap-5 rounded-xl transition-all hover:-translate-y-1 hover:shadow-2xl">
              <div className="flex justify-between items-start">
                <h3 className="text-xl font-semibold truncate max-w-[180px]" title={wallet.id}>
                  {wallet.id.slice(0, 8)}...
                </h3>
                <span className="px-2 py-1 rounded text-[11px] font-bold uppercase tracking-wider bg-emerald-500/15 text-emerald-500">
                  Mainnet
                </span>
              </div>

              <div className="my-2">
                <p className="text-2xl font-bold tracking-tight break-all">{formatBalance(wallet.balances) || '0.00'}</p>
              </div>

              <div className="flex items-center justify-between bg-black/20 p-3 rounded-lg border border-border">
                <code className="font-mono text-sm text-muted truncate">{wallet.public_key}</code>
                <button
                  onClick={() => navigator.clipboard.writeText(wallet.public_key)}
                  className="bg-transparent border-none text-muted hover:text-foreground hover:bg-white/10 p-1.5 rounded cursor-pointer transition-colors"
                  title="Copy Address"
                >
                  📋
                </button>
              </div>

              <div className="flex justify-between items-center mt-2 pt-4 border-t border-border">
                <a
                  href={`https://stellar.expert/explorer/public/account/${wallet.public_key}`}
                  target="_blank"
                  rel="noreferrer"
                  className="text-sm font-medium text-accent hover:text-accent-hover hover:underline transition-colors"
                >
                  View on Stellar Expert ↗
                </a>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
