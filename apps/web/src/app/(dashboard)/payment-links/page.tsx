'use client';

import { useCallback, useEffect, useState } from 'react';
import { Check, Copy, Link2, Plus, X } from 'lucide-react';
import { api } from '@/lib/api';
import type { PaymentLink } from '@/lib/types';
import { useToast } from '@/lib/toast-context';
import { PageHeader } from '@/components/ui/page-header';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Badge } from '@/components/ui/badge';

export default function PaymentLinksPage() {
  const { toast } = useToast();
  const [walletIds, setWalletIds] = useState<string[]>([]);
  const [links, setLinks] = useState<PaymentLink[]>([]);
  const [walletId, setWalletId] = useState('');
  const [amount, setAmount] = useState('');
  const [currency, setCurrency] = useState('NGN');
  const [expiresAt, setExpiresAt] = useState(() => {
    const date = new Date(Date.now() + 7 * 86400000);
    date.setMinutes(date.getMinutes() - date.getTimezoneOffset());
    return date.toISOString().slice(0, 16);
  });
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [result, wallets] = await Promise.all([api.listPaymentLinks(), api.listWallets()]);
      setLinks(result.payment_links || []);
      setWalletIds(wallets.wallets.map((wallet) => wallet.id));
      setWalletId((current) => current || wallets.wallets[0]?.id || '');
    } catch (error) {
      toast(error instanceof Error ? error.message : 'Could not load payment links', 'error');
    }
  }, [toast]);

  useEffect(() => {
    let cancelled = false;
    queueMicrotask(() => {
      if (!cancelled) void refresh();
    });
    return () => {
      cancelled = true;
    };
  }, [refresh]);

  const create = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    try {
      const link = await api.createPaymentLink({
        wallet_id: walletId,
        amount,
        currency,
        expires_at: new Date(expiresAt).toISOString(),
      });
      setLinks((current) => [link, ...current]);
      setAmount('');
      toast('Payment link created', 'success');
    } catch (error) {
      toast(error instanceof Error ? error.message : 'Could not create payment link', 'error');
    } finally {
      setBusy(false);
    }
  };

  const cancel = async (id: string) => {
    try {
      await api.cancelPaymentLink(id);
      setLinks((current) =>
        current.map((link) => (link.id === id ? { ...link, status: 'cancelled' } : link)),
      );
    } catch (error) {
      toast(error instanceof Error ? error.message : 'Could not cancel payment link', 'error');
    }
  };

  const copy = async (link: PaymentLink) => {
    const url = new URL(link.checkout_url, window.location.origin).toString();
    await navigator.clipboard.writeText(url);
    toast('Checkout link copied', 'success');
  };

  return (
    <div className="flex flex-col gap-6 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <PageHeader
        title="Payment Links"
        description="Create fixed-amount checkouts with an expiry."
      />
      <Card className="max-w-3xl">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Plus className="h-5 w-5" />
            New payment link
          </CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={create} className="grid gap-4 sm:grid-cols-2">
            <label className="flex flex-col gap-1.5 text-sm font-medium">
              Wallet
              <Select
                value={walletId}
                onChange={(event) => setWalletId(event.target.value)}
                required
              >
                <option value="">Select wallet</option>
                {walletIds.map((id) => (
                  <option key={id} value={id}>
                    {id.slice(0, 16)}...
                  </option>
                ))}
              </Select>
            </label>
            <label className="flex flex-col gap-1.5 text-sm font-medium">
              Amount
              <Input
                type="number"
                min="0.01"
                step="0.01"
                value={amount}
                onChange={(event) => setAmount(event.target.value)}
                required
              />
            </label>
            <label className="flex flex-col gap-1.5 text-sm font-medium">
              Currency
              <Select value={currency} onChange={(event) => setCurrency(event.target.value)}>
                <option value="NGN">NGN</option>
                <option value="KES">KES</option>
                <option value="GHS">GHS</option>
                <option value="ZAR">ZAR</option>
                <option value="UGX">UGX</option>
                <option value="TZS">TZS</option>
                <option value="USD">USD</option>
              </Select>
            </label>
            <label className="flex flex-col gap-1.5 text-sm font-medium">
              Expires
              <Input
                type="datetime-local"
                value={expiresAt}
                onChange={(event) => setExpiresAt(event.target.value)}
                required
              />
            </label>
            <div className="sm:col-span-2">
              <Button type="submit" isLoading={busy}>
                <Link2 className="mr-2 h-4 w-4" />
                Create link
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
      <section className="overflow-x-auto">
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Amount</TableHeader>
              <TableHeader>Checkout</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Expires</TableHeader>
              <TableHeader className="text-right">Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {links.map((link) => (
              <TableRow key={link.id}>
                <TableCell className="font-medium">
                  {link.currency} {link.amount}
                </TableCell>
                <TableCell className="max-w-56 truncate font-mono text-xs">
                  {link.checkout_url}
                </TableCell>
                <TableCell>
                  <Badge>{link.status}</Badge>
                </TableCell>
                <TableCell>{new Date(link.expires_at).toLocaleString()}</TableCell>
                <TableCell className="text-right">
                  <div className="flex justify-end gap-2">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-8 w-8 px-0"
                      aria-label="Copy checkout link"
                      title="Copy checkout link"
                      onClick={() => void copy(link)}
                    >
                      <Copy className="h-4 w-4" />
                    </Button>
                    {link.status === 'active' && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        className="h-8 w-8 px-0"
                        aria-label="Cancel payment link"
                        title="Cancel payment link"
                        onClick={() => void cancel(link.id)}
                      >
                        <X className="h-4 w-4" />
                      </Button>
                    )}
                    {link.status === 'paid' && (
                      <Check className="h-4 w-4 text-emerald-600" aria-label="Paid" />
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
            {links.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="py-10 text-center text-muted-foreground">
                  No payment links yet
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </section>
    </div>
  );
}
