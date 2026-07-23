'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useAuth } from '@/lib/auth';

const navItems = [
  { name: 'Overview', href: '/overview', icon: '📊' },
  { name: 'Wallets', href: '/wallets', icon: '💼' },
  { name: 'Transfers', href: '/transfers', icon: '💸' },
  { name: 'Conversions', href: '/conversions', icon: '🔄' },
  { name: 'API Keys', href: '/api-keys', icon: '🔑' },
  { name: 'Webhooks', href: '/webhooks', icon: '🪝' },
  { name: 'Usage', href: '/usage', icon: '📈' },
  { name: 'Team', href: '/team', icon: '👥' },
];

export default function Sidebar() {
  const pathname = usePathname();
  const router = useRouter();
  const { logout } = useAuth();

  const handleLogout = () => {
    logout();
    router.push('/login');
  };

  return (
    <aside className="w-[260px] h-screen fixed top-0 left-0 flex flex-col p-6 border-r border-border glass rounded-none z-50">
      <div className="flex items-center gap-2 mb-8 px-2">
        <span className="text-2xl font-bold text-foreground tracking-tight">Fluxa</span>
        <span className="text-[10px] font-bold uppercase bg-accent text-white px-1.5 py-0.5 rounded">Tenant</span>
      </div>
      <nav className="flex flex-col gap-2 flex-1">
        {navItems.map((item) => {
          const isActive = pathname?.startsWith(item.href);
          return (
            <Link
              key={item.name}
              href={item.href}
              className={`flex items-center gap-3 px-4 py-3 rounded-lg font-medium text-[15px] transition-all duration-200 ${
                isActive 
                  ? 'bg-accent/15 text-accent' 
                  : 'text-muted hover:bg-white/5 hover:text-foreground'
              }`}
            >
              <span className="text-lg">{item.icon}</span>
              {item.name}
            </Link>
          );
        })}
      </nav>
      <div className="mt-auto pt-4 border-t border-border">
        <button
          onClick={handleLogout}
          className="w-full p-3 bg-transparent border border-border text-muted rounded-lg font-medium transition-all duration-200 hover:bg-red-500/10 hover:text-red-500 hover:border-red-500 cursor-pointer"
        >
          Logout
        </button>
      </div>
    </aside>
  );
}
