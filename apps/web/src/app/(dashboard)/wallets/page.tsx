'use client';

import { useState } from 'react';

const mockWallets = [
  { id: 'wallet_1', name: 'Main Treasury', address: 'GCXKG...39L2', balance: '12,500.00 USDC', network: 'Mainnet' },
  { id: 'wallet_2', name: 'Operational Fund', address: 'GBH5R...82M1', balance: '4,200.00 XLM', network: 'Mainnet' },
  { id: 'wallet_3', name: 'Dev Test Wallet', address: 'GAT3W...19P4', balance: '10,000.00 XLM', network: 'Testnet' },
];

export default function WalletsPage() {
  const [wallets, setWallets] = useState(mockWallets);
  const [isCreating, setIsCreating] = useState(false);

  const handleCreateWallet = () => {
    setIsCreating(true);
    setTimeout(() => {
      setWallets([
        ...wallets,
        {
          id: `wallet_${Date.now()}`,
          name: 'New Wallet',
          address: 'GNEWX...' + Math.floor(Math.random() * 10000),
          balance: '0.00 XLM',
          network: 'Testnet'
        }
      ]);
      setIsCreating(false);
    }, 800);
  };

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex justify-between items-center gap-4">
        <div>
          <h1 className="text-[2rem] font-bold tracking-tight">Wallets</h1>
          <p className="text-muted text-[1.05rem] mt-1">Manage your Stellar wallets and balances.</p>
        </div>
        <button 
          className="bg-accent hover:bg-accent-hover text-white px-6 py-3 rounded-lg font-semibold shadow-[0_4px_12px_rgba(139,92,246,0.3)] transition-all hover:-translate-y-px disabled:opacity-70 disabled:cursor-not-allowed"
          onClick={handleCreateWallet}
          disabled={isCreating}
        >
          {isCreating ? 'Creating...' : '+ Create Wallet'}
        </button>
      </header>

      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-6">
        {wallets.map(wallet => (
          <div key={wallet.id} className="glass p-6 flex flex-col gap-5 rounded-xl transition-all hover:-translate-y-1 hover:shadow-2xl">
            <div className="flex justify-between items-start">
              <h3 className="text-xl font-semibold">{wallet.name}</h3>
              <span className={`px-2 py-1 rounded text-[11px] font-bold uppercase tracking-wider ${
                wallet.network === 'Mainnet' 
                  ? 'bg-emerald-500/15 text-emerald-500' 
                  : 'bg-amber-500/15 text-amber-500'
              }`}>
                {wallet.network}
              </span>
            </div>
            
            <div className="my-2">
              <p className="text-3xl font-bold tracking-tight">{wallet.balance}</p>
            </div>

            <div className="flex items-center justify-between bg-black/20 p-3 rounded-lg border border-border">
              <code className="font-mono text-sm text-muted">{wallet.address}</code>
              <button className="bg-transparent border-none text-muted hover:text-foreground hover:bg-white/10 p-1.5 rounded cursor-pointer transition-colors" title="Copy Address">
                📋
              </button>
            </div>

            <div className="flex justify-between items-center mt-2 pt-4 border-t border-border">
              {wallet.network === 'Testnet' ? (
                <button className="bg-transparent border border-border text-foreground px-4 py-2 rounded-lg font-medium text-sm hover:bg-white/5 hover:border-white/20 transition-colors">
                  Fund via Friendbot
                </button>
              ) : (
                <button className="bg-transparent border border-border text-foreground px-4 py-2 rounded-lg font-medium text-sm hover:bg-white/5 hover:border-white/20 transition-colors">
                  Deposit
                </button>
              )}
              <a href={`https://stellar.expert/explorer/public/account/${wallet.address}`} target="_blank" rel="noreferrer" className="text-sm font-medium text-accent hover:text-accent-hover hover:underline transition-colors">
                View on Stellar Expert ↗
              </a>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
