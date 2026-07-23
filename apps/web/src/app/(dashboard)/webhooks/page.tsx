'use client';

import { useState, useEffect } from 'react';
import { api, ApiClientError } from '@/lib/api';
import { WebhookEndpoint, WebhookDelivery } from '@/lib/types';

export default function WebhooksPage() {
  const [endpoints, setEndpoints] = useState<WebhookEndpoint[]>([]);
  const [deliveries, setDeliveries] = useState<WebhookDelivery[]>([]);
  const [url, setUrl] = useState('');
  const [selectedEvents, setSelectedEvents] = useState<string[]>([]);
  const [isCreating, setIsCreating] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');
  const [showCreate, setShowCreate] = useState(false);

  useEffect(() => {
    let mounted = true;

    api.listWebhooks()
      .then((eps) => {
        if (!mounted) return;
        setEndpoints(eps);
        if (eps.length > 0) {
          api.listWebhookDeliveries(eps[0].id)
            .then((dels) => { if (mounted) setDeliveries(dels); })
            .catch(() => {});
        }
      })
      .catch((err) => { if (mounted && err instanceof ApiClientError) setError(err.message); })
      .finally(() => { if (mounted) setIsLoading(false); });

    return () => { mounted = false; };
  }, []);

  const loadData = async () => {
    const eps = await api.listWebhooks();
    setEndpoints(eps);
    if (eps.length > 0) {
      const dels = await api.listWebhookDeliveries(eps[0].id);
      setDeliveries(dels);
    }
  };

  const handleRegister = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsCreating(true);
    setError('');

    try {
      await api.registerWebhook({
        url,
        events: selectedEvents,
      });
      setUrl('');
      setSelectedEvents([]);
      setShowCreate(false);
      await loadData();
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.message);
      }
    } finally {
      setIsCreating(false);
    }
  };

  const handleDelete = async (id: string) => {
    setError('');
    try {
      await api.deleteWebhook(id);
      await loadData();
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.message);
      }
    }
  };

  const toggleEvent = (event: string) => {
    setSelectedEvents((prev) =>
      prev.includes(event) ? prev.filter((e) => e !== event) : [...prev, event],
    );
  };

  const allEvents = ['transfer.initiated', 'transfer.settled', 'transfer.failed', 'wallet.funded'];

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="text-muted text-lg">Loading webhooks...</div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex justify-between items-center gap-4">
        <div>
          <h1 className="text-[2rem] font-bold tracking-tight">Webhooks</h1>
          <p className="text-muted text-[1.05rem] mt-1">Configure webhooks to receive real-time event notifications.</p>
        </div>
        <button
          onClick={() => setShowCreate(!showCreate)}
          className="bg-accent hover:bg-accent-hover text-white px-6 py-3 rounded-lg font-semibold transition-all hover:-translate-y-px cursor-pointer"
        >
          {showCreate ? 'Cancel' : '+ Register Webhook'}
        </button>
      </header>

      {error && (
        <div className="bg-red-500/10 border border-red-500/30 text-red-500 p-4 rounded-lg text-sm">
          {error}
        </div>
      )}

      {showCreate && (
        <div className="glass p-8 flex flex-col gap-6 max-w-[600px] rounded-2xl">
          <h3 className="text-xl font-semibold m-0">Register Webhook Endpoint</h3>
          <form onSubmit={handleRegister} className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium text-muted">Webhook URL</label>
              <input
                type="url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                required
                placeholder="https://your-domain.com/webhook"
                className="bg-black/20 border border-border px-4 py-3 rounded-lg text-foreground text-base outline-none focus:border-accent"
              />
            </div>

            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium text-muted">Events</label>
              <div className="flex flex-col gap-2 mt-1">
                {allEvents.map((event) => (
                  <label key={event} className="flex items-center gap-3 text-[15px] text-foreground cursor-pointer">
                    <input
                      type="checkbox"
                      checked={selectedEvents.includes(event)}
                      onChange={() => toggleEvent(event)}
                      className="w-[1.1rem] h-[1.1rem] accent-accent"
                    />
                    {event}
                  </label>
                ))}
              </div>
              {selectedEvents.length === 0 && (
                <p className="text-xs text-muted mt-1">Leave empty to subscribe to all events.</p>
              )}
            </div>

            <button
              type="submit"
              disabled={isCreating}
              className="bg-accent hover:bg-accent-hover text-white px-6 py-3 rounded-lg font-semibold transition-all hover:-translate-y-px disabled:opacity-70 disabled:cursor-not-allowed mt-2"
            >
              {isCreating ? 'Registering...' : 'Register'}
            </button>
          </form>
        </div>
      )}

      {endpoints.length > 0 && (
        <div className="flex flex-col gap-4">
          <h2 className="text-xl font-semibold m-0">Endpoints</h2>
          <div className="grid grid-cols-1 gap-4">
            {endpoints.map((ep) => (
              <div key={ep.id} className="glass p-6 flex items-center justify-between rounded-xl">
                <div className="flex flex-col gap-1">
                  <code className="font-mono text-sm text-foreground">{ep.url}</code>
                  <div className="flex gap-2 mt-1">
                    <span className="text-xs text-muted">{ep.events.length > 0 ? ep.events.join(', ') : 'All events'}</span>
                    <span className={`text-xs px-2 py-0.5 rounded-full ${ep.active ? 'bg-emerald-500/15 text-emerald-500' : 'bg-zinc-500/15 text-zinc-400'}`}>
                      {ep.active ? 'Active' : 'Inactive'}
                    </span>
                  </div>
                </div>
                <button
                  onClick={() => handleDelete(ep.id)}
                  className="bg-transparent text-red-500 border border-red-500/30 px-3 py-1.5 rounded-lg text-[13px] font-medium cursor-pointer transition-all hover:bg-red-500/10"
                >
                  Delete
                </button>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="flex flex-col gap-4">
        <h2 className="text-xl font-semibold m-0">Recent Delivery Logs</h2>
        {deliveries.length === 0 ? (
          <div className="glass p-8 rounded-xl text-center text-muted">
            No delivery logs yet.
          </div>
        ) : (
          <div className="glass rounded-xl overflow-hidden">
            <table className="w-full text-left border-collapse">
              <thead>
                <tr>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Event Type</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Status</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Response Code</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Attempts</th>
                  <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Time</th>
                </tr>
              </thead>
              <tbody>
                {deliveries.map((d) => (
                  <tr key={d.id} className="transition-colors hover:bg-white/5 border-b border-border last:border-0">
                    <td className="p-5 font-medium">{d.event_type}</td>
                    <td className="p-5">
                      <span className={`inline-block px-3 py-1 rounded-full text-[11px] font-bold uppercase tracking-wider ${
                        d.status === 'delivered' ? 'bg-emerald-500/15 text-emerald-500' : 'bg-red-500/15 text-red-500'
                      }`}>
                        {d.status}
                      </span>
                    </td>
                    <td className="p-5">
                      <span className={`inline-block px-3 py-1 rounded-full text-[11px] font-bold uppercase tracking-wider ${
                        d.response_code >= 200 && d.response_code < 300 ? 'bg-emerald-500/15 text-emerald-500' : 'bg-red-500/15 text-red-500'
                      }`}>
                        {d.response_code}
                      </span>
                    </td>
                    <td className="p-5 text-muted text-sm">{d.attempt_count}</td>
                    <td className="p-5 text-muted text-sm">{new Date(d.created_at).toLocaleString()}</td>
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
