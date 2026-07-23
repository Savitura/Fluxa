'use client';

import { useState, useEffect } from 'react';
import { api, ApiClientError } from '@/lib/api';
import { FeeSchedule } from '@/lib/types';
import { APIKey } from '@/lib/types';

export default function UsagePage() {
  const [feeSchedule, setFeeSchedule] = useState<FeeSchedule | null>(null);
  const [apiKeys, setApiKeys] = useState<APIKey[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    const fetchData = async () => {
      setIsLoading(true);
      setError('');
      try {
        const [feeData, keysData] = await Promise.all([
          api.getFeeSchedule().catch(() => null),
          api.listApiKeys().catch(() => [] as APIKey[]),
        ]);
        setFeeSchedule(feeData);
        setApiKeys(keysData);
      } catch (err) {
        if (err instanceof ApiClientError) {
          setError(err.message);
        }
      } finally {
        setIsLoading(false);
      }
    };
    fetchData();
  }, []);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="text-muted text-lg">Loading usage data...</div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex flex-col gap-2">
        <h1 className="text-[2rem] font-bold tracking-tight">Usage &amp; Billing</h1>
        <p className="text-muted text-[1.05rem]">Monitor your API usage and fee schedule.</p>
      </header>

      {error && (
        <div className="bg-red-500/10 border border-red-500/30 text-red-500 p-4 rounded-lg text-sm">
          {error}
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <div className="glass p-8 flex flex-col gap-4 rounded-2xl">
          <h3 className="text-xl font-semibold m-0">Active API Keys</h3>
          <p className="text-4xl font-bold tracking-tight m-0">
            {apiKeys.filter((k) => !k.revoked_at).length}
          </p>
          <div className="w-full h-2 bg-black/20 rounded-full overflow-hidden mt-2 border border-border" />
          <p className="text-sm text-muted mt-2">{apiKeys.length} total keys created.</p>
        </div>

        <div className="glass p-8 flex flex-col gap-4 rounded-2xl">
          <h3 className="text-xl font-semibold m-0">Transfer Fee</h3>
          <p className="text-4xl font-bold tracking-tight m-0">
            {feeSchedule ? `${feeSchedule.transfer_fee_bps} bps` : '—'}
          </p>
          <div className="w-full h-2 bg-black/20 rounded-full overflow-hidden mt-2 border border-border">
            {feeSchedule && (
              <div
                className="h-full bg-emerald-500 rounded-full"
                style={{ width: `${Math.min(feeSchedule.transfer_fee_bps, 100)}%` }}
              />
            )}
          </div>
          <p className="text-sm text-muted mt-2">
            {feeSchedule
              ? `Min: ${feeSchedule.min_fee_amount} ${feeSchedule.asset}`
              : 'No fee data available.'}
          </p>
        </div>
      </div>

      {feeSchedule && (
        <div className="glass p-8 flex flex-col gap-6 rounded-2xl">
          <h3 className="text-xl font-semibold m-0 border-b border-border pb-4">Fee Schedule Details</h3>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-6">
            <div className="flex flex-col gap-1">
              <span className="text-sm text-muted">Transfer Fee</span>
              <span className="text-2xl font-bold">{feeSchedule.transfer_fee_bps} bps</span>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-sm text-muted">Conversion Fee</span>
              <span className="text-2xl font-bold">{feeSchedule.conversion_fee_bps} bps</span>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-sm text-muted">Min Fee</span>
              <span className="text-2xl font-bold">{feeSchedule.min_fee_amount} {feeSchedule.asset}</span>
            </div>
            <div className="flex flex-col gap-1">
              <span className="text-sm text-muted">Max Fee</span>
              <span className="text-2xl font-bold">
                {feeSchedule.max_fee_amount ? `${feeSchedule.max_fee_amount} ${feeSchedule.asset}` : 'None'}
              </span>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
