import { useTenants, useFairness } from "@/api/hooks";
import { AlertTriangle } from "lucide-react";

export default function FairnessDashboard() {
  const tenants = useTenants();
  const fairness = useFairness();

  const data = tenants.data ?? MOCK_TENANTS;
  const noisyTenants = data.filter(t => t.is_noisy);
  const victimTenants = data.filter(t => t.is_victim);

  return (
    <div>
      <div style={{ marginBottom: "24px" }}>
        <h1 style={{ fontFamily: "var(--font-display)", fontSize: "24px", fontWeight: 700, color: "var(--color-text-primary)", margin: 0, letterSpacing: "-0.02em" }}>Fairness Dashboard</h1>
        <p style={{ color: "var(--color-text-muted)", fontSize: "13px", margin: "4px 0 0" }}>Tenant resource allocation and Jain''s fairness index</p>
      </div>

      {/* Noisy-neighbor banners */}
      {noisyTenants.map(t => (
        <Banner key={t.name} type="warning" message={`Noisy neighbor detected: ${t.name} is consuming disproportionate resources`} />
      ))}
      {victimTenants.map(t => (
        <Banner key={t.name} type="error" message={`Victim tenant: ${t.name} is below guaranteed share`} />
      ))}

      {/* Jain''s index stat */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: "12px", marginBottom: "20px" }}>
        <StatCard label="Global Fairness Index" value={fairness.data ? fairness.data.global_index.toFixed(3) : "0.841"} accent="cyan" />
        <StatCard label="Active Tenants" value={String(data.length)} accent="sky" />
        <StatCard label="Noisy Alerts" value={String(noisyTenants.length)} accent={noisyTenants.length > 0 ? "amber" : "emerald"} />
      </div>

      {/* Tenant allocation bars */}
      <div style={{ background: "var(--color-bg-surface)", border: "1px solid var(--color-border-default)", borderRadius: "12px", padding: "18px 20px", marginBottom: "16px" }}>
        <div style={{ fontFamily: "var(--font-display)", fontSize: "14px", fontWeight: 600, color: "var(--color-text-primary)", marginBottom: "16px" }}>
          Resource Allocation — CPU Cores
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
          {data.map(t => (
            <TenantBar key={t.name} tenant={t} />
          ))}
        </div>
      </div>

      {/* Per-tenant fairness scores */}
      <div style={{ background: "var(--color-bg-surface)", border: "1px solid var(--color-border-default)", borderRadius: "12px", padding: "18px 20px" }}>
        <div style={{ fontFamily: "var(--font-display)", fontSize: "14px", fontWeight: 600, color: "var(--color-text-primary)", marginBottom: "14px" }}>
          Per-Tenant Fairness Scores
        </div>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))", gap: "10px" }}>
          {data.map(t => {
            const score = fairness.data?.per_tenant?.[t.name] ?? t.fairness_score;
            const color = score > 0.9 ? "var(--color-emerald)" : score > 0.7 ? "var(--color-amber)" : "var(--color-rose)";
            return (
              <div key={t.name} style={{ background: "var(--color-bg-elevated)", border: "1px solid var(--color-border-subtle)", borderRadius: "8px", padding: "12px" }}>
                <div style={{ fontFamily: "var(--font-mono)", fontSize: "11px", color: "var(--color-text-muted)", marginBottom: "6px" }}>{t.name}</div>
                <div style={{ fontFamily: "var(--font-mono)", fontSize: "20px", fontWeight: 700, color }}>{score.toFixed(3)}</div>
                <TierBadge tier={t.tier} />
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function TenantBar({ tenant }: { tenant: typeof MOCK_TENANTS[0] }) {
  const guaranteed = (tenant.guaranteed_cores_percent / 100) * (tenant.max_cores || 16);
  const current = tenant.current_cores;
  const max = tenant.max_cores || 16;
  const guaranteedPct = Math.min(100, (guaranteed / max) * 100);
  const currentPct = Math.min(100, (current / max) * 100);
  const isOver = current > guaranteed;
  const statusColor = tenant.is_noisy ? "var(--color-amber)" : tenant.is_victim ? "var(--color-rose)" : "var(--color-emerald)";

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "6px" }}>
        <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
          <div style={{ width: "7px", height: "7px", borderRadius: "50%", background: statusColor, boxShadow: `0 0 6px ${statusColor}` }} />
          <span style={{ fontFamily: "var(--font-mono)", fontSize: "12px", color: "var(--color-text-secondary)" }}>{tenant.name}</span>
          <TierBadge tier={tenant.tier} />
        </div>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: "11px", color: isOver ? "var(--color-amber)" : "var(--color-text-muted)" }}>
          {current.toFixed(1)} / {max} cores
        </span>
      </div>
      <div style={{ height: "10px", background: "var(--color-bg-overlay)", borderRadius: "5px", overflow: "hidden", position: "relative" as const }}>
        {/* Guaranteed threshold marker */}
        <div style={{ position: "absolute" as const, left: `${guaranteedPct}%`, top: 0, bottom: 0, width: "1px", background: "rgba(255,255,255,0.2)", zIndex: 2 }} />
        {/* Current usage bar */}
        <div style={{
          height: "100%", width: `${currentPct}%`,
          background: `linear-gradient(90deg, ${isOver ? "var(--color-amber)" : "var(--color-cyan-glow)"}, ${isOver ? "var(--color-amber-dim)" : "var(--color-sky)"})`,
          borderRadius: "5px", transition: "width 0.4s ease",
        }} />
      </div>
      <div style={{ display: "flex", gap: "12px", marginTop: "4px" }}>
        <span style={{ fontSize: "10px", color: "var(--color-text-muted)" }}>guaranteed: {guaranteed.toFixed(1)}</span>
        <span style={{ fontSize: "10px", color: "var(--color-text-muted)" }}>max: {max}</span>
      </div>
    </div>
  );
}

function TierBadge({ tier }: { tier: string }) {
  const colors: Record<string, string> = { platinum: "var(--color-cyan-glow)", gold: "var(--color-amber)", silver: "var(--color-text-secondary)", bronze: "var(--color-text-muted)" };
  return (
    <span style={{ fontSize: "9px", padding: "1px 5px", borderRadius: "3px", border: `1px solid ${(colors[tier] ?? colors.bronze)}40`, color: colors[tier] ?? colors.bronze, fontFamily: "var(--font-mono)", textTransform: "uppercase" as const, letterSpacing: "0.06em" }}>{tier}</span>
  );
}

function StatCard({ label, value, accent }: { label: string; value: string; accent: string }) {
  const c: Record<string, string> = { cyan: "var(--color-cyan-glow)", sky: "var(--color-sky)", emerald: "var(--color-emerald)", amber: "var(--color-amber)" };
  const color = c[accent] ?? "var(--color-text-primary)";
  return (
    <div style={{ background: "var(--color-bg-surface)", border: "1px solid var(--color-border-default)", borderRadius: "10px", padding: "14px 16px" }}>
      <div style={{ fontSize: "11px", color: "var(--color-text-muted)", textTransform: "uppercase" as const, letterSpacing: "0.08em", fontFamily: "var(--font-mono)", marginBottom: "8px" }}>{label}</div>
      <div style={{ fontFamily: "var(--font-mono)", fontSize: "24px", fontWeight: 700, color }}>{value}</div>
    </div>
  );
}

function Banner({ type, message }: { type: "warning" | "error"; message: string }) {
  const bg = type === "warning" ? "rgba(245,158,11,0.08)" : "rgba(244,63,94,0.08)";
  const border = type === "warning" ? "rgba(245,158,11,0.3)" : "rgba(244,63,94,0.3)";
  const color = type === "warning" ? "var(--color-amber)" : "var(--color-rose)";
  return (
    <div style={{ display: "flex", alignItems: "center", gap: "10px", padding: "10px 14px", borderRadius: "8px", background: bg, border: `1px solid ${border}`, marginBottom: "10px", color }}>
      <AlertTriangle size={14} />
      <span style={{ fontSize: "13px" }}>{message}</span>
    </div>
  );
}

const MOCK_TENANTS = [
  { name: "team-alpha", tier: "platinum", current_cores: 12.4, max_cores: 16, guaranteed_cores_percent: 50, fairness_score: 0.95, is_noisy: false, is_victim: false, allocation_status: "bursting", current_memory_gib: 24, current_cost_usd: 120, max_memory_gib: 32, max_monthly_cost_usd: 3000, burstable: true, last_refreshed: new Date().toISOString() },
  { name: "team-beta", tier: "gold", current_cores: 4.1, max_cores: 8, guaranteed_cores_percent: 30, fairness_score: 0.88, is_noisy: true, is_victim: false, allocation_status: "bursting", current_memory_gib: 8, current_cost_usd: 45, max_memory_gib: 16, max_monthly_cost_usd: 1000, burstable: true, last_refreshed: new Date().toISOString() },
  { name: "team-gamma", tier: "silver", current_cores: 1.2, max_cores: 4, guaranteed_cores_percent: 25, fairness_score: 0.62, is_noisy: false, is_victim: true, allocation_status: "throttled", current_memory_gib: 4, current_cost_usd: 12, max_memory_gib: 8, max_monthly_cost_usd: 400, burstable: false, last_refreshed: new Date().toISOString() },
  { name: "team-delta", tier: "bronze", current_cores: 2.0, max_cores: 4, guaranteed_cores_percent: 20, fairness_score: 0.79, is_noisy: false, is_victim: false, allocation_status: "guaranteed", current_memory_gib: 4, current_cost_usd: 20, max_memory_gib: 8, max_monthly_cost_usd: 400, burstable: true, last_refreshed: new Date().toISOString() },
];