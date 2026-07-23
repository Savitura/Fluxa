'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { useAuth } from '@/lib/auth';
import { api, ApiClientError } from '@/lib/api';

export default function LoginPage() {
  const [apiKey, setApiKey] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const router = useRouter();
  const { login } = useAuth();

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsLoading(true);
    setError('');

    try {
      await api.health();
      login(apiKey);
      router.push('/overview');
    } catch (err) {
      if (err instanceof ApiClientError) {
        if (err.status === 401) {
          setError('Invalid API key. Please check and try again.');
        } else {
          setError(err.message);
        }
      } else {
        setError('Unable to connect to the API. Check your API URL.');
      }
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="flex items-center justify-center min-h-screen bg-[radial-gradient(circle_at_center,_var(--color-background)_0%,_#000_100%)]">
      <div className="w-full max-w-[400px] p-12 flex flex-col gap-8 glass animate-in fade-in slide-in-from-bottom-4 duration-500 rounded-2xl">
        <div className="text-center flex flex-col gap-2">
          <h1 className="text-3xl font-extrabold tracking-tight bg-gradient-to-br from-white to-zinc-500 bg-clip-text text-transparent">
            Fluxa Tenant
          </h1>
          <p className="text-muted text-[15px]">Sign in with your API key</p>
        </div>

        <form onSubmit={handleLogin} className="flex flex-col gap-6">
          <div className="flex flex-col gap-2">
            <label htmlFor="apiKey" className="text-sm font-medium text-muted">API Key</label>
            <input
              id="apiKey"
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              required
              className="bg-black/20 border border-border p-3 rounded-lg text-foreground focus:outline-none focus:border-accent focus:ring-2 focus:ring-accent/20 transition-all font-mono"
              placeholder="fx_..."
            />
          </div>

          {error && (
            <p className="text-red-500 text-sm text-center">{error}</p>
          )}

          <button
            type="submit"
            className="bg-accent hover:bg-accent-hover text-white border-none p-3.5 rounded-lg font-semibold cursor-pointer transition-all shadow-[0_4px_12px_rgba(139,92,246,0.3)] mt-2 hover:-translate-y-px disabled:opacity-70 disabled:cursor-not-allowed"
            disabled={isLoading}
          >
            {isLoading ? 'Signing in...' : 'Sign In'}
          </button>
        </form>
      </div>
    </div>
  );
}
