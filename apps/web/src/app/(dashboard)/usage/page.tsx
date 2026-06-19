'use client';

export default function UsagePage() {
  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header className="flex flex-col gap-2">
        <h1 className="text-[2rem] font-bold tracking-tight">Usage & Billing</h1>
        <p className="text-muted text-[1.05rem]">Monitor your API usage and limits.</p>
      </header>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <div className="glass p-8 flex flex-col gap-4 rounded-2xl">
          <h3 className="text-xl font-semibold m-0">API Calls</h3>
          <p className="text-4xl font-bold tracking-tight m-0">
            45,210 <span className="text-muted text-lg font-normal">/ 100,000</span>
          </p>
          <div className="w-full h-2 bg-black/20 rounded-full overflow-hidden mt-2 border border-border">
            <div className="h-full bg-accent rounded-full" style={{ width: '45%' }}></div>
          </div>
          <p className="text-sm text-muted mt-2">45% of your monthly quota used.</p>
        </div>

        <div className="glass p-8 flex flex-col gap-4 rounded-2xl">
          <h3 className="text-xl font-semibold m-0">Transfer Volume</h3>
          <p className="text-4xl font-bold tracking-tight m-0">
            $12,450.00
          </p>
          <div className="w-full h-2 bg-black/20 rounded-full overflow-hidden mt-2 border border-border">
            <div className="h-full bg-emerald-500 rounded-full" style={{ width: '12%' }}></div>
          </div>
          <p className="text-sm text-muted mt-2">No hard limit on transfer volume.</p>
        </div>
      </div>

      <div className="glass p-8 flex flex-col gap-6 rounded-2xl">
        <h3 className="text-xl font-semibold m-0 border-b border-border pb-4">Daily Usage Chart</h3>
        <div className="h-[300px] w-full bg-black/20 rounded-xl border border-border flex items-end justify-between p-6 gap-2">
          {/* Simple mock bar chart */}
          {[40, 20, 60, 30, 80, 50, 90, 45, 30, 65, 85, 40, 70, 55].map((height, i) => (
            <div key={i} className="w-full bg-accent/80 hover:bg-accent transition-colors rounded-t-sm" style={{ height: `${height}%` }}></div>
          ))}
        </div>
      </div>
    </div>
  );
}
