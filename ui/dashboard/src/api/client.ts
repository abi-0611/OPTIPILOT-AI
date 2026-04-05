import type {
  TenantState, FairnessResponse, UsageResponse,
  DecisionRecord, JournalStats,
  SimulationResult, SLOCostCurveResponse,
} from "./types";

const BASE = "";

async function json<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + url, {
    headers: { "Content-Type": "application/json", ...init?.headers },
    ...init,
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

// -- Tenant API ----------------------------------------------------------------
export const api = {
  tenants: {
    list: () => json<TenantState[]>("/api/v1/tenants"),
    get: (name: string) => json<TenantState>(`/api/v1/tenants/${name}`),
    usage: (name: string) => json<UsageResponse>(`/api/v1/tenants/${name}/usage`),
    fairness: () => json<FairnessResponse>("/api/v1/fairness"),
  },
  decisions: {
    list: (params?: { service?: string; namespace?: string; trigger?: string; limit?: number }) => {
      const q = new URLSearchParams();
      if (params?.service) q.set("service", params.service);
      if (params?.namespace) q.set("namespace", params.namespace);
      if (params?.trigger) q.set("trigger", params.trigger);
      if (params?.limit) q.set("limit", String(params.limit));
      return json<DecisionRecord[]>(`/api/v1/decisions?${q}`);
    },
    get: (id: string) => json<DecisionRecord>(`/api/v1/decisions/${id}`),
    explain: (id: string) => json<{ id: string; narrative: string }>(`/api/v1/decisions/${id}/explain`),
    summary: (window = "24h") => json<JournalStats>(`/api/v1/decisions/summary?window=${window}`),
    search: (q: string, limit = 20) => json<DecisionRecord[]>(`/api/v1/decisions/search?q=${encodeURIComponent(q)}&limit=${limit}`),
  },
  simulate: {
    run: (body: object) => json<SimulationResult>("/api/v1/simulate", { method: "POST", body: JSON.stringify(body) }),
    get: (id: string) => json<SimulationResult>(`/api/v1/simulate/${id}`),
    sloCostCurve: (body: object) => json<SLOCostCurveResponse>("/api/v1/simulate/slo-cost-curve", { method: "POST", body: JSON.stringify(body) }),
  },
};