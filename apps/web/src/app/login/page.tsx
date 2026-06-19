'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';

export default function LoginPage() {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const router = useRouter();

  const handleLogin = (e: React.FormEvent) => {
    e.preventDefault();
    setIsLoading(true);
    
    // Simulate API call to get JWT
    setTimeout(() => {
      setIsLoading(false);
      router.push('/overview');
    }, 1000);
  };

  return (
    <div className="flex items-center justify-center min-h-screen bg-[radial-gradient(circle_at_center,_var(--color-background)_0%,_#000_100%)]">
      <div className="w-full max-w-[400px] p-12 flex flex-col gap-8 glass animate-in fade-in slide-in-from-bottom-4 duration-500 rounded-2xl">
        <div className="text-center flex flex-col gap-2">
          <h1 className="text-3xl font-extrabold tracking-tight bg-gradient-to-br from-white to-zinc-500 bg-clip-text text-transparent">
            Fluxa Tenant
          </h1>
          <p className="text-muted text-[15px]">Sign in to your control plane</p>
        </div>

        <form onSubmit={handleLogin} className="flex flex-col gap-6">
          <div className="flex flex-col gap-2">
            <label htmlFor="email" className="text-sm font-medium text-muted">Email Address</label>
            <input
              id="email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              className="bg-black/20 border border-border p-3 rounded-lg text-foreground focus:outline-none focus:border-accent focus:ring-2 focus:ring-accent/20 transition-all"
              placeholder="admin@example.com"
            />
          </div>

          <div className="flex flex-col gap-2">
            <label htmlFor="password" className="text-sm font-medium text-muted">Password</label>
            <input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              className="bg-black/20 border border-border p-3 rounded-lg text-foreground focus:outline-none focus:border-accent focus:ring-2 focus:ring-accent/20 transition-all"
              placeholder="••••••••"
            />
          </div>

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
