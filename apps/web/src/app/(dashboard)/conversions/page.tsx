'use client';

import { useState } from 'react';
import { api, ApiClientError } from '@/lib/api';
import { FxQuoteResponse, Conversion } from '@/lib/types';

export default function ConversionsPage() {
  const [fromAsset, setFromAsset] = useState('USDC');
  const [toAsset, setToAsset] = useState('EURC');
  const [amount, setAmount] = useState('');
  const [walletId, setWalletId] = useState('');
  const [quote, setQuote] = useState<FxQuoteResponse | null>(null);
  const [result, setResult] = useState<Conversion | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isConverting, setIsConverting] = useState(false);
  const [error, setError] = useState('');

  const handleGetQuote = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsLoading(true);
    setError('');
    setResult(null);

    try {
      const q = await api.getFxQuote({ from_asset: fromAsset, to_asset: toAsset, amount });
      setQuote(q);
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.message);
      } else {
        setError('Failed to get quote');
      }
    } finally {
      setIsLoading(false);
    }
  };

  const handleConvert = async () => {
    if (!quote) return;
    setIsConverting(true);
    setError('');

    try {
      const conv = await api.executeConversion({ wallet_id: walletId, quote_id: '' });
      setResult(conv);
      setQuote(null);
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.message);
      } else {
        setError('Conversion failed');
      }
    } finally {
      setIsConverting(false);
    }
  };

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header>
        <h1 className="text-[2rem] font-bold tracking-tight">Conversions</h1>
        <p className="text-muted text-[1.05rem] mt-1">Convert between assets with real-time FX rates.</p>
      </header>

      <div className="glass p-8 flex flex-col gap-6 max-w-[600px] rounded-2xl">
        <h3 className="text-xl font-semibold m-0">New Conversion</h3>

        <form onSubmit={handleGetQuote} className="flex flex-col gap-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium text-muted">From Asset</label>
              <select
                value={fromAsset}
                onChange={(e) => setFromAsset(e.target.value)}
                className="bg-black/20 border border-border px-4 py-3 rounded-lg text-foreground text-base outline-none focus:border-accent"
              >
                <option value="USDC" className="bg-zinc-900">USDC</option>
                <option value="EURC" className="bg-zinc-900">EURC</option>
                <option value="XLM" className="bg-zinc-900">XLM</option>
              </select>
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium text-muted">To Asset</label>
              <select
                value={toAsset}
                onChange={(e) => setToAsset(e.target.value)}
                className="bg-black/20 border border-border px-4 py-3 rounded-lg text-foreground text-base outline-none focus:border-accent"
              >
                <option value="EURC" className="bg-zinc-900">EURC</option>
                <option value="USDC" className="bg-zinc-900">USDC</option>
                <option value="XLM" className="bg-zinc-900">XLM</option>
              </select>
            </div>
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

          <div className="flex flex-col gap-2">
            <label className="text-sm font-medium text-muted">Wallet ID</label>
            <input
              type="text"
              value={walletId}
              onChange={(e) => setWalletId(e.target.value)}
              required
              placeholder="uuid"
              className="bg-black/20 border border-border px-4 py-3 rounded-lg text-foreground text-base outline-none focus:border-accent font-mono"
            />
          </div>

          {error && <p className="text-red-500 text-sm">{error}</p>}

          <button
            type="submit"
            disabled={isLoading}
            className="bg-accent hover:bg-accent-hover text-white px-6 py-3 rounded-lg font-semibold transition-all hover:-translate-y-px disabled:opacity-70 disabled:cursor-not-allowed"
          >
            {isLoading ? 'Getting Quote...' : 'Get Quote'}
          </button>
        </form>
      </div>

      {quote && (
        <div className="glass p-8 flex flex-col gap-4 max-w-[600px] rounded-2xl">
          <h3 className="text-xl font-semibold m-0 border-b border-border pb-4">Quote</h3>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div className="text-muted">Rate</div>
            <div className="font-medium text-right">{quote.rate}</div>
            <div className="text-muted">Source Amount</div>
            <div className="font-medium text-right">{quote.source_amount}</div>
            <div className="text-muted">Dest Amount</div>
            <div className="font-medium text-right">{quote.dest_amount}</div>
            <div className="text-muted">Fee</div>
            <div className="font-medium text-right">{quote.fee_amount}</div>
            <div className="text-muted">Net Amount</div>
            <div className="font-medium text-right">{quote.net_amount}</div>
            <div className="text-muted">Spread (bps)</div>
            <div className="font-medium text-right">{quote.spread_bps}</div>
          </div>
          <button
            onClick={handleConvert}
            disabled={isConverting}
            className="bg-emerald-600 hover:bg-emerald-500 text-white px-6 py-3 rounded-lg font-semibold transition-all hover:-translate-y-px disabled:opacity-70 disabled:cursor-not-allowed mt-2"
          >
            {isConverting ? 'Converting...' : 'Execute Conversion'}
          </button>
        </div>
      )}

      {result && (
        <div className="glass p-8 flex flex-col gap-4 max-w-[600px] rounded-2xl">
          <h3 className="text-xl font-semibold m-0 border-b border-border pb-4">Conversion Result</h3>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div className="text-muted">ID</div>
            <div className="font-mono text-right">{result.id}</div>
            <div className="text-muted">From</div>
            <div className="font-medium text-right">{result.source_asset} {result.source_amount}</div>
            <div className="text-muted">To</div>
            <div className="font-medium text-right">{result.dest_asset} {result.dest_amount}</div>
            <div className="text-muted">Rate</div>
            <div className="font-medium text-right">{result.rate}</div>
            <div className="text-muted">Fee</div>
            <div className="font-medium text-right">{result.fee_amount}</div>
          </div>
        </div>
      )}
    </div>
  );
}
