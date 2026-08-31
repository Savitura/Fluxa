// Add status types and API methods to api.ts
export interface Incident {
  id: string;
  title: string;
  description: string;
  severity: string;
  status: string;
  created_at: string;
  resolved_at?: string;
}

export interface StatusResponse {
  api_version: string;
  status: string;
  message: string;
  recent_incidents: Incident[];
}

// Inside api object:
// getStatus: async (): Promise<StatusResponse> => {
//   const res = await fetch(`${API_URL}/status`);
//   if (!res.ok) throw new Error('Failed to fetch status');
//   return res.json();
// },
// listIncidents: async (): Promise<{ incidents: Incident[] }> => {
//   const res = await fetch(`${API_URL}/status/incidents`);
//   if (!res.ok) throw new Error('Failed to fetch incidents');
//   return res.json();
// },
