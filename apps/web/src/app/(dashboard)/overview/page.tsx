'use client';

// Mock data
const overviewData = {
  totalBalance: '$24,500.00',
  transferVolume: '$12,450.00',
  apiCalls: '45,210',
  apiLimit: '100,000',
  recentTransactions: [
    { id: 'tx_123', type: 'Transfer', amount: '+$500.00', date: '2 hours ago', status: 'Completed' },
    { id: 'tx_124', type: 'Withdrawal', amount: '-$1,200.00', date: '5 hours ago', status: 'Completed' },
    { id: 'tx_125', type: 'Deposit', amount: '+$3,000.00', date: '1 day ago', status: 'Completed' },
  ]
};

export default function OverviewPage() {
  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex flex-col gap-2">
        <h1 className="text-[2rem] font-bold tracking-tight">Dashboard Overview</h1>
        <p className="text-muted text-[1.05rem]">Welcome back. Here's what's happening today.</p>
      </header>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        <div className="glass p-6 flex flex-col gap-3 rounded-xl transition-all hover:-translate-y-1 hover:shadow-2xl">
          <h3 className="text-sm font-medium text-muted uppercase tracking-wider">Total Balance</h3>
          <p className="text-4xl font-bold tracking-tight">{overviewData.totalBalance}</p>
        </div>
        <div className="glass p-6 flex flex-col gap-3 rounded-xl transition-all hover:-translate-y-1 hover:shadow-2xl">
          <h3 className="text-sm font-medium text-muted uppercase tracking-wider">Transfer Volume (This Month)</h3>
          <p className="text-4xl font-bold tracking-tight">{overviewData.transferVolume}</p>
        </div>
        <div className="glass p-6 flex flex-col gap-3 rounded-xl transition-all hover:-translate-y-1 hover:shadow-2xl">
          <h3 className="text-sm font-medium text-muted uppercase tracking-wider">API Calls</h3>
          <p className="text-4xl font-bold tracking-tight">
            {overviewData.apiCalls} <span className="text-muted text-sm font-normal">/ {overviewData.apiLimit}</span>
          </p>
          <div className="w-full h-1.5 bg-border rounded-full overflow-hidden mt-2">
            <div className="h-full bg-accent rounded-full" style={{ width: '45%' }}></div>
          </div>
        </div>
      </div>

      <div className="flex flex-col gap-4">
        <h2 className="text-xl font-semibold">Recent Transactions</h2>
        <div className="glass rounded-xl overflow-hidden">
          <table className="w-full text-left border-collapse">
            <thead>
              <tr>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Transaction ID</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Type</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Amount</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Date</th>
                <th className="p-5 border-b border-border font-medium text-muted text-[13px] uppercase tracking-wider">Status</th>
              </tr>
            </thead>
            <tbody>
              {overviewData.recentTransactions.map(tx => (
                <tr key={tx.id} className="transition-colors hover:bg-white/5 border-b border-border last:border-0">
                  <td className="p-5 text-muted text-sm">{tx.id}</td>
                  <td className="p-5 text-sm">{tx.type}</td>
                  <td className={`p-5 text-sm font-medium ${tx.amount.startsWith('+') ? 'text-emerald-500' : 'text-red-500'}`}>
                    {tx.amount}
                  </td>
                  <td className="p-5 text-muted text-sm">{tx.date}</td>
                  <td className="p-5">
                    <span className="inline-block px-3 py-1 bg-emerald-500/15 text-emerald-500 rounded-full text-xs font-semibold uppercase">
                      {tx.status}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
