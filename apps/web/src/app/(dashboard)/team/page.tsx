'use client';

import { useState } from 'react';

const mockMembers = [
  { id: 'm1', name: 'Alice Admin', email: 'alice@example.com', role: 'Owner' },
  { id: 'm2', name: 'Bob Builder', email: 'bob@example.com', role: 'Developer' },
  { id: 'm3', name: 'Charlie Cash', email: 'charlie@example.com', role: 'Finance' },
];

export default function TeamPage() {
  const [members] = useState(mockMembers);
  const [isInviting, setIsInviting] = useState(false);

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex justify-between items-center gap-4">
        <div>
          <h1 className="text-[2rem] font-bold tracking-tight">Team Management</h1>
          <p className="text-muted text-[1.05rem] mt-1">Manage organization members and roles.</p>
        </div>
        <button 
          className="bg-accent hover:bg-accent-hover text-white px-6 py-3 rounded-lg font-semibold shadow-[0_4px_12px_rgba(139,92,246,0.3)] transition-all hover:-translate-y-px"
          onClick={() => setIsInviting(true)}
        >
          + Invite Member
        </button>
      </header>

      {isInviting && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-[100] flex items-center justify-center animate-in fade-in duration-200">
          <div className="glass w-full max-w-[500px] p-8 flex flex-col gap-6 rounded-2xl shadow-2xl">
            <h2 className="text-2xl font-bold m-0">Invite Team Member</h2>
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium text-muted">Email Address</label>
              <input type="email" placeholder="colleague@example.com" className="bg-black/20 border border-border p-3 rounded-lg text-foreground focus:outline-none focus:border-accent" />
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium text-muted">Role</label>
              <select className="bg-black/20 border border-border p-3 rounded-lg text-foreground focus:outline-none focus:border-accent">
                <option value="developer" className="bg-zinc-900">Developer</option>
                <option value="finance" className="bg-zinc-900">Finance</option>
                <option value="admin" className="bg-zinc-900">Admin</option>
              </select>
            </div>
            <div className="flex gap-4 mt-2">
              <button className="flex-1 bg-zinc-800 hover:bg-zinc-700 text-foreground p-3 rounded-xl font-medium transition-all" onClick={() => setIsInviting(false)}>
                Cancel
              </button>
              <button className="flex-1 bg-accent hover:bg-accent-hover text-white p-3 rounded-xl font-medium transition-all" onClick={() => setIsInviting(false)}>
                Send Invite
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="glass rounded-xl overflow-hidden">
        <table className="w-full text-left border-collapse">
          <thead>
            <tr>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">User</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Email</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Role</th>
              <th className="p-5 border-b border-border"></th>
            </tr>
          </thead>
          <tbody>
            {members.map(m => (
              <tr key={m.id} className="transition-colors hover:bg-white/5 border-b border-border last:border-0">
                <td className="p-5 font-medium flex items-center gap-3">
                  <div className="w-8 h-8 rounded-full bg-accent/20 flex items-center justify-center text-accent font-bold text-sm">
                    {m.name.charAt(0)}
                  </div>
                  {m.name}
                </td>
                <td className="p-5 text-muted text-sm">{m.email}</td>
                <td className="p-5">
                  <span className={`inline-block px-3 py-1 rounded-full text-[11px] font-bold uppercase tracking-wider ${
                    m.role === 'Owner' ? 'bg-purple-500/15 text-purple-400' : 
                    m.role === 'Developer' ? 'bg-blue-500/15 text-blue-400' : 
                    'bg-emerald-500/15 text-emerald-500'
                  }`}>
                    {m.role}
                  </span>
                </td>
                <td className="p-5 text-right">
                  {m.role !== 'Owner' && (
                    <button className="bg-transparent text-muted hover:text-red-500 border border-border hover:border-red-500 hover:bg-red-500/10 px-3.5 py-1.5 rounded-lg text-[13px] font-medium transition-all">
                      Remove
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
