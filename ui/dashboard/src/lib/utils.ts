import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

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