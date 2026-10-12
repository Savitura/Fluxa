'use client';

import { FormEvent, useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import { ArrowUpRight, CircleAlert, LoaderCircle } from 'lucide-react';

const API_BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:3000';

interface PublicPaymentLink {
  amount: string;
  currency: string;
  status: string;
  expires_at: string;
}

export default function HostedPaymentPage() {
  const params = useParams<{ token: string }>();
  const token = params.token;
  const [link, setLink] = useState<PublicPaymentLink | null>(null);
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    fetch(`${API_BASE}/v1/public/payment-links/${encodeURIComponent(token)}`)
      .then(async (response) => {
        if (!response.ok) throw new Error('This checkout link is unavailable.');
        return response.json() as Promise<PublicPaymentLink>;
      })
      .then((result) => {
        if (!cancelled) setLink(result);
      })
      .catch((reason: unknown) => {
        if (!cancelled)
          setError(reason instanceof Error ? reason.message : 'Could not load checkout.');
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  const checkout = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError('');
    try {
      const response = await fetch(
        `${API_BASE}/v1/public/payment-links/${encodeURIComponent(token)}/checkout`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ email, name }),
        },
      );
      const result = await response.json();
      if (!response.ok) throw new Error(result.error?.message || 'Could not start checkout.');
      window.location.assign(result.payment_link);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Could not start checkout.');
      setBusy(false);
    }
  };

  const unavailable = !link || link.status !== 'active' || new Date(link.expires_at) <= new Date();

  return (
    <main className="min-h-screen bg-[radial-gradient(ellipse_at_70%_0%,rgba(20,112,91,0.12),transparent_45%),linear-gradient(145deg,#f4f7f2,#edf2f4)] px-5 py-12 text-slate-900">
      <div className="mx-auto max-w-lg">
        <div className="mb-12 flex items-center gap-3">
          <div className="grid h-10 w-10 place-items-center rounded-md bg-emerald-800 text-white">
            F
          </div>
          <span className="font-semibold">Fluxa Checkout</span>
        </div>
        <div className="mb-8 border-b border-slate-300 pb-8">
          <p className="mb-3 text-sm font-semibold uppercase text-emerald-800">Secure payment</p>
          <h1 className="text-3xl font-semibold">Complete your payment</h1>
          {link && (
            <p className="mt-5 font-mono text-4xl font-semibold">
              {link.currency} {link.amount}
            </p>
          )}
        </div>
        {unavailable ? (
          <div
            role="status"
            className="flex items-center gap-3 border border-amber-300 bg-white/75 p-4 text-amber-950"
          >
            <CircleAlert className="h-5 w-5 shrink-0" />
            <span>{error || 'This checkout link is expired or has already been used.'}</span>
          </div>
        ) : (
          <form onSubmit={checkout} className="flex flex-col gap-5">
            <label className="flex flex-col gap-2 text-sm font-medium">
              Full name
              <input
                autoComplete="name"
                className="h-11 border border-slate-300 bg-white px-3 outline-none focus:border-emerald-700"
                value={name}
                onChange={(event) => setName(event.target.value)}
                required
              />
            </label>
            <label className="flex flex-col gap-2 text-sm font-medium">
              Email address
              <input
                type="email"
                autoComplete="email"
                className="h-11 border border-slate-300 bg-white px-3 outline-none focus:border-emerald-700"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                required
              />
            </label>
            {error && (
              <p role="alert" className="text-sm text-red-700">
                {error}
              </p>
            )}
            <button
              disabled={busy}
              className="flex h-12 items-center justify-center gap-2 bg-emerald-800 px-4 font-semibold text-white hover:bg-emerald-900 disabled:opacity-60"
            >
              {busy ? (
                <LoaderCircle className="h-4 w-4 animate-spin" />
              ) : (
                <>
                  Continue to payment <ArrowUpRight className="h-4 w-4" />
                </>
              )}
            </button>
            <p className="text-center text-xs text-slate-500">
              Payment details are handled securely by our payment provider.
            </p>
          </form>
        )}
      </div>
    </main>
  );
}
