'use client';

import { useState, useEffect } from 'react';
import { api, ApiClientError } from '@/lib/api';
import { APIKey } from '@/lib/types';

export default function ApiKeysPage() {
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [isCreating, setIsCreating] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [newKey, setNewKey] = useState<{ raw: string; prefix: string } | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let mounted = true;

    api.listApiKeys()
      .then((data) => { if (mounted) setKeys(data); })
      .catch((err) => { if (mounted && err instanceof ApiClientError) setError(err.message); })
      .finally(() => { if (mounted) setIsLoading(false); });

    return () => { mounted = false; };
  }, []);

  const handleCreateKey = async () => {
    setIsCreating(true);
    setError('');
    try {
      const data = await api.createApiKey();
      setNewKey({ raw: data.key, prefix: data.prefix });
      const updatedKeys = await api.listApiKeys();
      setKeys(updatedKeys);
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.message);
      }
    } finally {
      setIsCreating(false);
    }
  };

  const handleRevoke = async (id: string) => {
    setError('');
    try {
      await api.revokeApiKey(id);
      const updatedKeys = await api.listApiKeys();
      setKeys(updatedKeys);
    } catch (err) {
      if (err instanceof ApiClientError) {
        setError(err.message);
      }
    }
  };

  const closeModal = () => setNewKey(null);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="text-muted text-lg">Loading API keys...</div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex justify-between items-center gap-4">
        <div>
          <h1 className="text-[2rem] font-bold tracking-tight">API Keys</h1>
          <p className="text-muted text-[1.05rem] mt-1">Manage your API keys for authenticating requests to Fluxa.</p>
        </div>
        <button
          className="bg-accent hover:bg-accent-hover text-white px-6 py-3 rounded-lg font-semibold shadow-[0_4px_12px_rgba(139,92,246,0.3)] transition-all hover:-translate-y-px disabled:opacity-70 disabled:cursor-not-allowed cursor-pointer"
          onClick={handleCreateKey}
          disabled={isCreating}
        >
          {isCreating ? 'Generating...' : '+ Create Secret Key'}
        </button>
      </header>

      {error && (
        <div className="bg-red-500/10 border border-red-500/30 text-red-500 p-4 rounded-lg text-sm">
          {error}
        </div>
      )}

      {newKey && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-[100] flex items-center justify-center animate-in fade-in duration-200">
          <div className="glass w-full max-w-[500px] p-10 flex flex-col gap-6 rounded-2xl shadow-2xl">
            <h2 className="text-2xl font-bold m-0">API Key Created</h2>
            <p className="m-0 text-muted leading-relaxed">
              Please copy this key and save it somewhere safe. For security reasons, <strong className="text-foreground">we cannot show it to you again.</strong>
            </p>

            <div className="flex items-center justify-between bg-black/30 p-4 rounded-xl border border-accent">
              <code className="font-mono text-base break-all">{newKey.raw}</code>
              <button
                onClick={() => navigator.clipboard.writeText(newKey.raw)}
                className="bg-white/10 hover:bg-white/20 border-none text-foreground px-4 py-2 rounded-lg cursor-pointer font-medium transition-colors ml-4"
              >
                Copy
              </button>
            </div>

            <button
              className="bg-zinc-800 hover:bg-zinc-700 border border-border text-foreground p-3.5 rounded-xl cursor-pointer font-semibold transition-all mt-2"
              onClick={closeModal}
            >
              I have saved my key
            </button>
          </div>
        </div>
      )}

      {keys.length === 0 ? (
        <div className="glass p-12 flex flex-col items-center gap-4 rounded-2xl text-center">
          <p className="text-4xl">🔑</p>
          <h3 className="text-xl font-semibold">No API Keys</h3>
          <p className="text-muted max-w-md">Create your first API key to authenticate requests.</p>
        </div>
      ) : (
        <div className="glass rounded-xl overflow-hidden">
          <table className="w-full text-left border-collapse">
            <thead>
              <tr>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Label</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Token</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Created</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Last Used</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Status</th>
                <th className="p-5 border-b border-border"></th>
              </tr>
            </thead>
            <tbody>
              {keys.map((k) => (
                <tr key={k.id} className={`transition-colors hover:bg-white/5 border-b border-border last:border-0 ${k.revoked_at ? 'opacity-50' : ''}`}>
                  <td className="p-5 font-medium">{k.label || '—'}</td>
                  <td className="p-5">
                    <code className="font-mono bg-black/20 px-2 py-1.5 rounded border border-border text-[13px]">
                      {k.prefix}••••••••••••
                    </code>
                  </td>
                  <td className="p-5 text-muted text-sm">{new Date(k.created_at).toLocaleDateString()}</td>
                  <td className="p-5 text-muted text-sm">
                    {k.last_used_at ? new Date(k.last_used_at).toLocaleDateString() : 'Never'}
                  </td>
                  <td className="p-5">
                    <span className={`inline-block px-3 py-1 rounded-full text-[11px] font-bold uppercase tracking-wider ${
                      k.revoked_at ? 'bg-white/10 text-muted' : 'bg-emerald-500/15 text-emerald-500'
                    }`}>
                      {k.revoked_at ? 'Revoked' : 'Active'}
                    </span>
                  </td>
                  <td className="p-5 text-right">
                    {!k.revoked_at && (
                      <button
                        onClick={() => handleRevoke(k.id)}
                        className="bg-transparent text-red-500 border border-red-500/30 px-3.5 py-1.5 rounded-lg text-[13px] font-medium cursor-pointer transition-all hover:bg-red-500/10 hover:border-red-500"
                      >
                        Revoke
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
