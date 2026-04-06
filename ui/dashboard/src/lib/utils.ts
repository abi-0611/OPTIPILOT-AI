import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import type { DecisionRecord } from "@/api/types";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatDuration(iso: string): string {
  return iso;
}

export function formatCost(usd: number): string {
  return `$${usd.toLocaleString("en-US", { maximumFractionDigits: 2 })}`;
}

export function formatPercent(val: number, decimals = 1): string {
  return `${val.toFixed(decimals)}%`;
}

/** Human-readable replica change for dashboard rows (e.g. "2 → 1 replicas"). */
export function formatScalingSummary(record: DecisionRecord): string | null {
  const target = record.selectedAction?.targetReplica;
  if (target === undefined || target === null) return null;
  const before = record.currentState?.replicas ?? record.currentState?.Replicas;
  if (before !== undefined && before !== target) {
    return `${before} → ${target} replicas`;
  }
  return `Target ${target} replicas`;
}

/** One-line proactive signal from forecast / heuristic (when present). */
export function formatForecastHint(record: DecisionRecord): string | null {
  const f = record.forecastState;
  if (f == null || f.changePercent === undefined || f.changePercent === null) return null;
  const sign = f.changePercent >= 0 ? "+" : "";
  const conf =
    f.confidence !== undefined && f.confidence !== null
      ? ` · conf ${Math.round(f.confidence * 100)}%`
      : "";
  return `Demand forecast ${sign}${f.changePercent.toFixed(0)}%${conf}`;
}