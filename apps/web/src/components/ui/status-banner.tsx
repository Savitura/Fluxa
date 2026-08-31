'use client';

import { useEffect, useState } from 'react';
import { api, type StatusResponse } from '@/lib/api';
import { AlertTriangle, CheckCircle2, XCircle } from 'lucide-react';

export function StatusBanner() {
  const [statusData, setStatusData] = useState<StatusResponse | null>(null);

  useEffect(() => {
    api.getStatus().then(setStatusData).catch(() => {});
  }, []);

  if (!statusData || statusData.status === 'operational') {
    return null;
  }

  const isOutage = statusData.status === 'outage';

  return (
    <div className={`flex items-center justify-center gap-2 px-4 py-2 text-xs font-medium text-white ${isOutage ? 'bg-danger' : 'bg-warning'}`}>
      {isOutage ? <XCircle className="h-4 w-4" /> : <AlertTriangle className="h-4 w-4" />}
      <span>
        System Status: <strong className="capitalize">{statusData.status}</strong> — {statusData.message}
      </span>
    </div>
  );
}
