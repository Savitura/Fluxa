'use client';

import { useEffect, useState, useCallback } from 'react';
import { api, type WebhookEndpoint, type WebhookDelivery } from '@/lib/api';
import { useToast } from '@/lib/toast-context';
import { PageHeader } from '@/components/ui/page-header';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
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
import { VerifySignatureTool } from '@/components/webhooks/verify-signature-tool';
import { Webhook, Plus, X, Trash2, Key, RefreshCw } from 'lucide-react';

const eventOptions = [
  'transfer.initiated',
  'transfer.settled',
  'transfer.failed',
  'wallet.funded',
  'conversion.completed',
];

function deliveryStatusBadge(status: string) {
  if (status === 'success') return <Badge variant="success">{status}</Badge>;
  return <Badge variant="danger">{status}</Badge>;
}

export default function WebhooksPage() {
  const { toast } = useToast();
  const [endpoints, setEndpoints] = useState<WebhookEndpoint[]>([]);
  const [deliveries, setDeliveries] = useState<WebhookDelivery[]>([]);
  const [loading, setLoading] = useState(true);
  const [url, setUrl] = useState('');
  const [selectedEvents, setSelectedEvents] = useState<string[]>([
    'transfer.settled',
    'transfer.failed',
    'wallet.funded',
  ]);
  const [registering, setRegistering] = useState(false);
  const [showForm, setShowForm] = useState(false);
  const [signingSecret, setSigningSecret] = useState<string | null>(null);
  const [showSecret, setShowSecret] = useState(false);
  const [rotating, setRotating] = useState(false);

  const fetchEndpoints = useCallback(async () => {
    setLoading(true);
    try {
      const [res, secretRes] = await Promise.all([
        api.listWebhooks(),
        api.getWebhookSecret().catch(() => ({ signing_secret: '' })),
      ]);
      setEndpoints(res.endpoints || []);
      if (secretRes.signing_secret) {
        setSigningSecret(secretRes.signing_secret);
      }
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Failed to load webhooks', 'error');
    } finally {
      setLoading(false);
    }
  }, [toast]);

  useEffect(() => {
    let cancelled = false;
    const run = async () => {
      if (cancelled) return;
      await fetchEndpoints();
    };
    run();
    return () => {
      cancelled = true;
    };
  }, [fetchEndpoints]);

  const handleRegister = async (e: React.FormEvent) => {
    e.preventDefault();
    setRegistering(true);
    try {
      await api.registerWebhook(url, selectedEvents);
      toast('Webhook registered', 'success');
      setShowForm(false);
      setUrl('');
      await fetchEndpoints();
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Failed to register webhook', 'error');
    } finally {
      setRegistering(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm('Delete this webhook endpoint?')) return;
    try {
      await api.deleteWebhook(id);
      toast('Webhook deleted', 'success');
      await fetchEndpoints();
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Failed to delete webhook', 'error');
    }
  };

  const handleRotateSecret = async () => {
    if (!confirm('Rotating the signing secret will invalidate all current webhook signatures. Continue?')) return;
    setRotating(true);
    try {
      const res = await api.rotateWebhookSecret();
      setSigningSecret(res.signing_secret);
      setShowSecret(true);
      toast('Webhook signing secret rotated', 'success');
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Failed to rotate secret', 'error');
    } finally {
      setRotating(false);
    }
  };

  const toggleEvent = (event: string) => {
    setSelectedEvents((prev) =>
      prev.includes(event) ? prev.filter((e) => e !== event) : [...prev, event]
    );
  };

  if (loading) {
    return (
      <div className="flex flex-col gap-8">
        <div className="flex items-center justify-between">
          <Skeleton className="h-10 w-48" />
          <Skeleton className="h-10 w-36" />
        </div>
        <Skeleton className="h-64" />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-8 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <PageHeader
        title="Webhooks"
        description="Configure webhooks to receive real-time event notifications."
      >
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
              <Plus className="h-4 w-4" /> Add Endpoint
            </>
          )}
        </Button>
      </PageHeader>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle className="flex items-center gap-2">
                <Key className="h-5 w-5" /> Signing Secret
              </CardTitle>
              <CardDescription>Used to sign all outbound webhooks with HMAC-SHA256.</CardDescription>
            </div>
            <Button
              variant="secondary"
              size="sm"
              isLoading={rotating}
              onClick={handleRotateSecret}
            >
              <RefreshCw className="h-4 w-4" /> Rotate Secret
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          <div className="flex items-center gap-3">
            <Input
              type={showSecret ? 'text' : 'password'}
              value={signingSecret || ''}
              readOnly
              className="font-mono max-w-md"
            />
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setShowSecret(!showSecret)}
            >
              {showSecret ? 'Hide' : 'Reveal'}
            </Button>
          </div>
        </CardContent>
      </Card>

      {showForm && (
        <Card className="max-w-2xl">
          <CardHeader>
            <CardTitle>Register Webhook Endpoint</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleRegister} className="flex flex-col gap-5">
              <div className="flex flex-col gap-1.5">
                <label className="text-sm font-medium text-foreground">Webhook URL</label>
                <Input
                  type="url"
                  value={url}
                  onChange={(e) => setUrl(e.target.value)}
                  required
                  placeholder="https://your-domain.com/webhook"
                />
              </div>
              <div className="flex flex-col gap-2">
                <label className="text-sm font-medium text-foreground">Events to send</label>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  {eventOptions.map((event) => (
                    <label
                      key={event}
                      className="flex items-center gap-3 text-sm text-foreground cursor-pointer"
                    >
                      <input
                        type="checkbox"
                        checked={selectedEvents.includes(event)}
                        onChange={() => toggleEvent(event)}
                        className="h-4 w-4 rounded border-border text-primary accent-primary"
                      />
                      {event}
                    </label>
                  ))}
                </div>
              </div>
              <div className="flex justify-end">
                <Button type="submit" isLoading={registering} disabled={!url}>
                  Register Endpoint
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      )}

      <div className="grid grid-cols-1 gap-8">
        <VerifySignatureTool />
      </div>

      {endpoints.length > 0 && (
        <div className="flex flex-col gap-4">
          <h2 className="text-lg font-semibold">Registered Endpoints</h2>
          <div className="grid grid-cols-1 gap-4">
            {endpoints.map((ep) => (
              <Card key={ep.id}>
                <CardContent className="flex flex-col gap-4 p-5 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex flex-col gap-1 min-w-0">
                    <code className="truncate font-mono text-sm text-foreground">
                      {ep.url}
                    </code>
                    <span className="text-xs text-muted-foreground">
                      Events:{' '}
                      {ep.events.length > 0 ? ep.events.join(', ') : 'All'}
                    </span>
                  </div>
                  <div className="flex items-center gap-3">
                    <Badge variant={ep.active ? 'success' : 'default'}>
                      {ep.active ? 'Active' : 'Inactive'}
                    </Badge>
                    <Button
                      variant="danger"
                      size="sm"
                      onClick={() => handleDelete(ep.id)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
