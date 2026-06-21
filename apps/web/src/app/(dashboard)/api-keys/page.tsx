'use client';

import { useState } from 'react';

const mockApiKeys = [
  { id: 'key_1', prefix: 'pk_test_a8f9', label: 'Development Key', created: '2023-10-12', lastUsed: '2 hours ago', status: 'Active' },
  { id: 'key_2', prefix: 'pk_live_d7b2', label: 'Production Key', created: '2023-11-05', lastUsed: '5 mins ago', status: 'Active' },
  { id: 'key_3', prefix: 'pk_test_11c4', label: 'Old Integration', created: '2023-08-20', lastUsed: '3 months ago', status: 'Revoked' },
];

export default function ApiKeysPage() {
  const [keys, setKeys] = useState(mockApiKeys);
  const [isCreating, setIsCreating] = useState(false);
  const [newKey, setNewKey] = useState<string | null>(null);

  const handleCreateKey = () => {
    setIsCreating(true);
    setTimeout(() => {
      const generatedRaw = 'pk_test_' + Math.random().toString(36).substring(2, 15) + Math.random().toString(36).substring(2, 15);
      const prefix = generatedRaw.substring(0, 12);
      
      setKeys([
        {
          id: `key_${Date.now()}`,
          prefix,
          label: 'New API Key',
          created: 'Just now',
          lastUsed: 'Never',
          status: 'Active'
        },
        ...keys
      ]);
      setNewKey(generatedRaw);
      setIsCreating(false);
    }, 600);
  };

  const closeModal = () => setNewKey(null);

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex justify-between items-center gap-4">
        <div>
          <h1 className="text-[2rem] font-bold tracking-tight">API Keys</h1>
          <p className="text-muted text-[1.05rem] mt-1">Manage your API keys for authenticating requests to Fluxa.</p>
        </div>
        <button 
          className="bg-accent hover:bg-accent-hover text-white px-6 py-3 rounded-lg font-semibold shadow-[0_4px_12px_rgba(139,92,246,0.3)] transition-all hover:-translate-y-px disabled:opacity-70 disabled:cursor-not-allowed"
          onClick={handleCreateKey}
          disabled={isCreating}
        >
          {isCreating ? 'Generating...' : '+ Create Secret Key'}
        </button>
      </header>

      {newKey && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-[100] flex items-center justify-center animate-in fade-in duration-200">
          <div className="glass w-full max-w-[500px] p-10 flex flex-col gap-6 rounded-2xl shadow-2xl">
            <h2 className="text-2xl font-bold m-0">API Key Created</h2>
            <p className="m-0 text-muted leading-relaxed">
              Please copy this key and save it somewhere safe. For security reasons, <strong className="text-foreground">we cannot show it to you again.</strong>
            </p>
            
            <div className="flex items-center justify-between bg-black/30 p-4 rounded-xl border border-accent">
              <code className="font-mono text-base break-all">{newKey}</code>
              <button className="bg-white/10 hover:bg-white/20 border-none text-foreground px-4 py-2 rounded-lg cursor-pointer font-medium transition-colors ml-4">
                Copy
              </button>
            </div>
            
            <button className="bg-zinc-800 hover:bg-zinc-700 border border-border text-foreground p-3.5 rounded-xl cursor-pointer font-semibold transition-all mt-2" onClick={closeModal}>
              I have saved my key
            </button>
          </div>
        </div>
      )}

      <div className="glass rounded-xl overflow-hidden">
        <table className="w-full text-left border-collapse">
          <thead>
            <tr>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Name</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Token</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Created</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Last Used</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Status</th>
              <th className="p-5 border-b border-border"></th>
            </tr>
          </thead>
          <tbody>
            {keys.map(k => (
              <tr key={k.id} className={`transition-colors hover:bg-white/5 border-b border-border last:border-0 ${k.status === 'Revoked' ? 'opacity-50' : ''}`}>
                <td className="p-5 font-medium">{k.label}</td>
                <td className="p-5">
                  <code className="font-mono bg-black/20 px-2 py-1.5 rounded border border-border text-[13px]">
                    {k.prefix}••••••••••••
                  </code>
                </td>
                <td className="p-5 text-muted text-sm">{k.created}</td>
                <td className="p-5 text-muted text-sm">{k.lastUsed}</td>
                <td className="p-5">
                  <span className={`inline-block px-3 py-1 rounded-full text-[11px] font-bold uppercase tracking-wider ${
                    k.status === 'Active' ? 'bg-emerald-500/15 text-emerald-500' : 'bg-white/10 text-muted'
                  }`}>
                    {k.status}
                  </span>
                </td>
                <td className="p-5 text-right">
                  {k.status === 'Active' && (
                    <button className="bg-transparent text-red-500 border border-red-500/30 px-3.5 py-1.5 rounded-lg text-[13px] font-medium cursor-pointer transition-all hover:bg-red-500/10 hover:border-red-500">
                      Revoke
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
