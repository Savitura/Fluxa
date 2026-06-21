'use client';

import { useState } from 'react';

const mockTransfers = [
  { id: 'tr_8f92bd', amount: '500.00 USDC', from: 'wallet_1', to: 'external_addr', status: 'Completed', date: '2023-11-05 14:30:00', hash: 'fb92...' },
  { id: 'tr_2a19cd', amount: '1,200.00 XLM', from: 'external_addr', to: 'wallet_2', status: 'Completed', date: '2023-11-04 09:15:22', hash: 'a1b2...' },
  { id: 'tr_9d81ef', amount: '3,000.00 USDC', from: 'wallet_1', to: 'wallet_3', status: 'Pending', date: '2023-11-05 16:45:10', hash: 'pending' },
];

export default function TransfersPage() {
  const [transfers] = useState(mockTransfers);
  const [filter, setFilter] = useState('All');

  const filteredTransfers = filter === 'All' ? transfers : transfers.filter(t => t.status === filter);

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex justify-between items-center gap-4">
        <div>
          <h1 className="text-[2rem] font-bold tracking-tight">Transfers</h1>
          <p className="text-muted text-[1.05rem] mt-1">View and trace your transfer history.</p>
        </div>
        <div className="flex gap-4">
          <select 
            className="bg-black/20 border border-border px-4 py-2.5 rounded-lg text-foreground text-[15px] cursor-pointer outline-none focus:border-accent"
            value={filter} 
            onChange={(e) => setFilter(e.target.value)}
          >
            <option value="All" className="bg-zinc-900 text-foreground">All Statuses</option>
            <option value="Completed" className="bg-zinc-900 text-foreground">Completed</option>
            <option value="Pending" className="bg-zinc-900 text-foreground">Pending</option>
            <option value="Failed" className="bg-zinc-900 text-foreground">Failed</option>
          </select>
        </div>
      </header>

      <div className="glass rounded-xl overflow-hidden">
        <table className="w-full text-left border-collapse">
          <thead>
            <tr>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">ID</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Amount</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">From / To</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Date</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Status</th>
              <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Stellar Tx</th>
            </tr>
          </thead>
          <tbody>
            {filteredTransfers.map(tr => (
              <tr key={tr.id} className="transition-colors hover:bg-white/5 border-b border-border last:border-0">
                <td className="p-5 text-muted text-sm">{tr.id}</td>
                <td className="p-5 font-medium">{tr.amount}</td>
                <td className="p-5 text-muted text-sm">
                  {tr.from} &rarr; {tr.to}
                </td>
                <td className="p-5 text-muted text-sm">{tr.date}</td>
                <td className="p-5">
                  <span className={`inline-block px-3 py-1 rounded-full text-[11px] font-bold uppercase tracking-wider ${
                    tr.status === 'Completed' ? 'bg-emerald-500/15 text-emerald-500' : 
                    tr.status === 'Pending' ? 'bg-amber-500/15 text-amber-500' : 
                    'bg-red-500/15 text-red-500'
                  }`}>
                    {tr.status}
                  </span>
                </td>
                <td className="p-5">
                  {tr.hash !== 'pending' ? (
                    <a href={`https://stellar.expert/explorer/public/tx/${tr.hash}`} target="_blank" rel="noreferrer" className="text-accent font-medium text-sm hover:text-accent-hover hover:underline">
                      {tr.hash} ↗
                    </a>
                  ) : (
                    <span className="text-muted">—</span>
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
