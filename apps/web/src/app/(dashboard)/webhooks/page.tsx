'use client';

import { useState } from 'react';

const mockLogs = [
  { id: 'evt_1', event: 'transfer.completed', endpoint: 'https://api.example.com/webhooks/fluxa', status: 200, time: '2 mins ago' },
  { id: 'evt_2', event: 'wallet.funded', endpoint: 'https://api.example.com/webhooks/fluxa', status: 200, time: '1 hour ago' },
  { id: 'evt_3', event: 'transfer.failed', endpoint: 'https://api.example.com/webhooks/fluxa', status: 500, time: '3 hours ago' },
];

export default function WebhooksPage() {
  const [logs] = useState(mockLogs);
  const [endpoint, setEndpoint] = useState('https://api.example.com/webhooks/fluxa');
  const [isEditing, setIsEditing] = useState(false);

  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex justify-between items-center gap-4">
        <div>
          <h1 className="text-[2rem] font-bold tracking-tight">Webhooks</h1>
          <p className="text-muted text-[1.05rem] mt-1">Configure webhooks to receive real-time event notifications.</p>
        </div>
      </header>

      <div className="glass p-8 flex flex-col gap-6 max-w-[800px] rounded-2xl">
        <div className="flex justify-between items-center border-b border-border pb-4">
          <h3 className="text-xl font-semibold m-0">Endpoint Configuration</h3>
          <button 
            className="bg-accent hover:bg-accent-hover text-white px-4 py-2 rounded-lg font-medium cursor-pointer transition-colors"
            onClick={() => setIsEditing(!isEditing)}
          >
            {isEditing ? 'Save' : 'Edit'}
          </button>
        </div>
        
        <div className="flex flex-col gap-2">
          <label className="text-sm font-medium text-muted">Webhook URL</label>
          <input 
            type="text" 
            value={endpoint} 
            onChange={(e) => setEndpoint(e.target.value)}
            disabled={!isEditing}
            className="bg-black/20 border border-border px-4 py-3 rounded-lg text-foreground text-base transition-colors focus:outline-none focus:border-accent disabled:opacity-60 disabled:cursor-not-allowed"
            placeholder="https://your-domain.com/webhook"
          />
        </div>

        <div className="flex flex-col gap-2 mt-2">
          <label className="text-sm font-medium text-muted">Events to send</label>
          <div className="flex flex-col gap-3 mt-2">
            <label className="flex items-center gap-3 text-[15px] text-foreground cursor-pointer">
              <input type="checkbox" defaultChecked disabled={!isEditing} className="w-[1.1rem] h-[1.1rem] accent-accent" /> transfer.completed
            </label>
            <label className="flex items-center gap-3 text-[15px] text-foreground cursor-pointer">
              <input type="checkbox" defaultChecked disabled={!isEditing} className="w-[1.1rem] h-[1.1rem] accent-accent" /> transfer.failed
            </label>
            <label className="flex items-center gap-3 text-[15px] text-foreground cursor-pointer">
              <input type="checkbox" defaultChecked disabled={!isEditing} className="w-[1.1rem] h-[1.1rem] accent-accent" /> wallet.funded
            </label>
          </div>
        </div>
      </div>

      <div className="flex flex-col gap-4">
        <h2 className="text-xl font-semibold m-0">Recent Delivery Logs</h2>
        <div className="glass rounded-xl overflow-hidden">
          <table className="w-full text-left border-collapse">
            <thead>
              <tr>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Event Type</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Endpoint</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Response Status</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Time</th>
              </tr>
            </thead>
            <tbody>
              {logs.map(log => (
                <tr key={log.id} className="transition-colors hover:bg-white/5 border-b border-border last:border-0">
                  <td className="p-5 font-medium">{log.event}</td>
                  <td className="p-5 text-muted text-sm">{log.endpoint}</td>
                  <td className="p-5">
                    <span className={`inline-block px-3 py-1 rounded-full text-[11px] font-bold uppercase tracking-wider ${
                      log.status === 200 ? 'bg-emerald-500/15 text-emerald-500' : 'bg-red-500/15 text-red-500'
                    }`}>
                      {log.status}
                    </span>
                  </td>
                  <td className="p-5 text-muted text-sm">{log.time}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
