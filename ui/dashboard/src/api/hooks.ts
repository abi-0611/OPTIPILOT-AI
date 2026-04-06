import { useQuery, useMutation } from "@tanstack/react-query";
import { api } from "./client";

export const useMeta = () =>
  useQuery({ queryKey: ["meta"], queryFn: api.meta.get, staleTime: 60_000 });

export const useServiceObjectives = () =>
  useQuery({
    queryKey: ["service-objectives"],
    queryFn: api.serviceObjectives.list,
    refetchInterval: 15_000,
  });

// -- Tenant hooks --------------------------------------------------------------
export const useTenants = () =>
  useQuery({ queryKey: ["tenants"], queryFn: api.tenants.list, refetchInterval: 30_000 });

export const useTenant = (name: string) =>
  useQuery({ queryKey: ["tenants", name], queryFn: () => api.tenants.get(name), enabled: !!name });

export const useTenantUsage = (name: string) =>
  useQuery({ queryKey: ["tenants", name, "usage"], queryFn: () => api.tenants.usage(name), enabled: !!name });

export const useFairness = () =>
  useQuery({ queryKey: ["fairness"], queryFn: api.tenants.fairness, refetchInterval: 30_000 });

// -- Decision hooks ------------------------------------------------------------
export const useDecisions = (params?: { service?: string; namespace?: string; trigger?: string; limit?: number }) =>
  useQuery({ queryKey: ["decisions", params], queryFn: () => api.decisions.list(params), refetchInterval: 5_000 });

export const useDecision = (id: string) =>
  useQuery({ queryKey: ["decisions", id], queryFn: () => api.decisions.get(id), enabled: !!id });

export const useDecisionExplain = (id: string) =>
  useQuery({ queryKey: ["decisions", id, "explain"], queryFn: () => api.decisions.explain(id), enabled: !!id });

export const useDecisionSummary = (window = "24h") =>
  useQuery({ queryKey: ["decisions", "summary", window], queryFn: () => api.decisions.summary(window), refetchInterval: 30_000 });

export const useDecisionSearch = (q: string) =>
  useQuery({ queryKey: ["decisions", "search", q], queryFn: () => api.decisions.search(q), enabled: q.length >= 2 });

// -- Simulation hooks ----------------------------------------------------------
export const useRunSimulation = () =>
  useMutation({ mutationFn: api.simulate.run });

export const useSimulationResult = (id: string) =>
  useQuery({ queryKey: ["simulation", id], queryFn: () => api.simulate.get(id), enabled: !!id });

export const useSLOCostCurve = () =>
  useMutation({ mutationFn: api.simulate.sloCostCurve });