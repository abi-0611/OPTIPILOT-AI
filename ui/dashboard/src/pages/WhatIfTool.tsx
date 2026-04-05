import { useState } from "react";
import { useRunSimulation, useSLOCostCurve } from "@/api/hooks";
import { FlaskConical, BarChart2, Loader2 } from "lucide-react";

const SERVICES = ["api-gateway", "payment-service", "worker", "inventory", "analytics", "auth", "notification", "search"];
const RANGES = ["1h", "6h", "24h", "7d"] as const;

export default function WhatIfTool() {
  const [form, setForm] = useState({
    service: SERVICES[0],
    description: "",
    timeRange: "24h" as typeof RANGES[number],
    sloTarget: 0.995,
    dryRun: true,
  });
  const sim = useRunSimulation();
  const curve = useSLOCostCurve();

  function handleRun(e: React.FormEvent) {
    e.preventDefault();
    sim.mutate(
      { service: form.service, description: form.description, time_range: form.timeRange, slo_target: form.sloTarget, dry_run: form.dryRun },
      {}
    );
  }

  function handleCurve() {
    curve.mutate({ service: form.service, slo_metric: "availability", points: 8 });
  }

  return (
    <div>
      <div style={{ marginBottom: "24px" }}>
        <h1 style={{ fontFamily: "var(--font-display)", fontSize: "24px", fontWeight: 700, color: "var(--color-text-primary)", margin: 0, letterSpacing: "-0.02em" }}>What-If Simulator</h1>
        <p style={{ color: "var(--color-text-muted)", fontSize: "13px", margin: "4px 0 0" }}>Run counter-factual scenarios and explore the SLO-cost trade-off curve</p>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "340px 1fr", gap: "20px", alignItems: "start" }}>
        {/* Form */}
        <div style={{ background: "var(--color-bg-surface)", border: "1px solid var(--color-border-subtle)", borderRadius: "12px", padding: "20px" }}>
          <div style={sectionLabel}>Simulation Parameters</div>
          <form onSubmit={handleRun} style={{ display: "flex", flexDirection: "column" as const, gap: "14px", marginTop: "12px" }}>
            <FormField label="Service">
              <select aria-label="Service" value={form.service} onChange={e => setForm({ ...form, service: e.target.value })} style={inputStyle}>
                {SERVICES.map(s => <option key={s} value={s}>{s}</option>)}
              </select>
            </FormField>

            <FormField label="Time Range">
              <div style={{ display: "flex", gap: "6px" }}>
                {RANGES.map(r => (
                  <button key={r} type="button" onClick={() => setForm({ ...form, timeRange: r })}
                    style={{ flex: 1, padding: "7px 0", fontSize: "12px", fontFamily: "var(--font-mono)", borderRadius: "6px", border: "1px solid", cursor: "pointer",
                      background: form.timeRange === r ? "rgba(34,211,238,0.1)" : "transparent",
                      borderColor: form.timeRange === r ? "var(--color-cyan-glow)" : "var(--color-border-subtle)",
                      color: form.timeRange === r ? "var(--color-cyan-glow)" : "var(--color-text-muted)" }}>
                    {r}
                  </button>
                ))}
              </div>
            </FormField>

            <FormField label={`SLO Target: ${(form.sloTarget * 100).toFixed(2)}%`}>
              <input type="range" aria-label="SLO target" min="0.99" max="0.9999" step="0.0001" value={form.sloTarget}
                onChange={e => setForm({ ...form, sloTarget: parseFloat(e.target.value) })}
                style={{ width: "100%", accentColor: "var(--color-cyan-glow)" }}
              />
            </FormField>

            <FormField label="Description (optional)">
              <input aria-label="Simulation description" value={form.description} onChange={e => setForm({ ...form, description: e.target.value })}
                placeholder="What scenario are you testing?"
                style={{ ...inputStyle, width: "100%", boxSizing: "border-box" as const }}
              />
            </FormField>

            <label style={{ display: "flex", alignItems: "center", gap: "8px", cursor: "pointer" }}>
              <input type="checkbox" checked={form.dryRun} onChange={e => setForm({ ...form, dryRun: e.target.checked })} style={{ accentColor: "var(--color-cyan-glow)" }} />
              <span style={{ fontSize: "13px", color: "var(--color-text-secondary)" }}>Dry run (no actual changes)</span>
            </label>

            <button type="submit" disabled={sim.isPending}
              style={{ display: "flex", alignItems: "center", justifyContent: "center", gap: "8px", padding: "10px 0", background: "rgba(34,211,238,0.1)", border: "1px solid var(--color-cyan-glow)", borderRadius: "8px", color: "var(--color-cyan-glow)", fontFamily: "var(--font-mono)", fontSize: "13px", fontWeight: 600, cursor: "pointer" }}>
              {sim.isPending ? <Loader2 size={14} className="animate-spin" /> : <FlaskConical size={14} />}
              {sim.isPending ? "Running..." : "Run Simulation"}
            </button>
          </form>

          <div style={{ marginTop: "14px", height: "1px", background: "var(--color-border-subtle)" }} />

          <button onClick={handleCurve} disabled={curve.isPending}
            style={{ marginTop: "14px", width: "100%", display: "flex", alignItems: "center", justifyContent: "center", gap: "8px", padding: "9px 0", background: "rgba(245,158,11,0.08)", border: "1px solid rgba(245,158,11,0.3)", borderRadius: "8px", color: "var(--color-amber)", fontFamily: "var(--font-mono)", fontSize: "12px", cursor: "pointer" }}>
            {curve.isPending ? <Loader2 size={13} className="animate-spin" /> : <BarChart2 size={13} />}
            Generate SLO-Cost Curve
          </button>
        </div>

        {/* Results */}
        <div style={{ display: "flex", flexDirection: "column" as const, gap: "16px" }}>
          {/* Sim result */}
          {(sim.data || MOCK_SIM_RESULT) && (
            <SimResultCard result={(sim.data as typeof MOCK_SIM_RESULT | undefined) ?? MOCK_SIM_RESULT} />
          )}

          {/* SLO-cost curve */}
          {(curve.data || MOCK_CURVE) && (
            <CurveCard points={curve.data?.points ?? MOCK_CURVE.points} service={curve.data?.service ?? form.service} />
          )}

          {!sim.data && !curve.data && (
            <div style={{ display: "flex", flexDirection: "column" as const, alignItems: "center", justifyContent: "center", gap: "10px", height: "240px", color: "var(--color-text-muted)", border: "1px dashed var(--color-border-subtle)", borderRadius: "12px" }}>
              <FlaskConical size={28} style={{ opacity: 0.4 }} />
              <span style={{ fontSize: "13px", fontFamily: "var(--font-body)" }}>Run a simulation or generate a curve to see results</span>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function SimResultCard({ result }: { result: typeof MOCK_SIM_RESULT }) {
  const delta = result.cost_delta_percent ?? 0;
  const saved = delta < 0;
  const breaches = result.slo_breaches ?? 0;
  return (
    <div style={{ background: "var(--color-bg-surface)", border: "1px solid var(--color-border-subtle)", borderRadius: "12px", padding: "20px" }}>
      <div style={sectionLabel}>Simulation Result</div>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr 1fr", gap: "12px", marginTop: "14px" }}>
        <MetricBox label="Original Cost/hr" value={`$${result.original_cost?.monthly_usd ?? "—"}`} />
        <MetricBox label="Simulated Cost/hr" value={`$${result.simulated_cost?.monthly_usd ?? "—"}`} />
        <MetricBox label="Cost Delta" value={`${saved ? "" : "+"}${delta.toFixed(1)}%`} highlight={saved ? "var(--color-emerald)" : "var(--color-rose)"} />
        <MetricBox label="SLO Breaches" value={String(breaches)} highlight={breaches === 0 ? "var(--color-emerald)" : "var(--color-amber)"} />
      </div>
      {result.description && (
        <div style={{ marginTop: "12px", fontSize: "12px", color: "var(--color-text-muted)", fontStyle: "italic" as const }}>{result.description}</div>
      )}
    </div>
  );
}

function CurveCard({ points, service }: { points: typeof MOCK_CURVE.points; service: string }) {
  const maxCost = Math.max(...points.map(p => p.projected_monthly_cost));
  return (
    <div style={{ background: "var(--color-bg-surface)", border: "1px solid var(--color-border-subtle)", borderRadius: "12px", padding: "20px" }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "16px" }}>
        <div style={sectionLabel}>SLO-Cost Trade-off Curve � {service}</div>
        <span style={{ fontSize: "10px", fontFamily: "var(--font-mono)", color: "var(--color-text-muted)" }}>monthly USD vs SLO target</span>
      </div>
      <div style={{ display: "flex", flexDirection: "column" as const, gap: "8px" }}>
        {[...points].sort((a, b) => b.slo_target - a.slo_target).map((p, i) => (
          <div key={i} style={{ display: "grid", gridTemplateColumns: "90px 1fr 70px", gap: "10px", alignItems: "center" }}>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: "12px", color: "var(--color-text-muted)" }}>{(p.slo_target * 100).toFixed(2)}%</span>
            <div style={{ height: "22px", background: "var(--color-bg-overlay)", borderRadius: "4px", overflow: "hidden" }}>
              <div style={{ height: "100%", width: `${(p.projected_monthly_cost / maxCost) * 100}%`, background: "linear-gradient(90deg, var(--color-cyan-glow), var(--color-sky))", borderRadius: "4px", display: "flex", alignItems: "center", paddingLeft: "6px" }}>
                <span style={{ fontSize: "10px", fontFamily: "var(--font-mono)", color: "var(--color-bg-base)", fontWeight: 700 }}>{p.avg_replicas}r</span>
              </div>
            </div>
            <span style={{ fontFamily: "var(--font-mono)", fontSize: "12px", color: "var(--color-text-primary)", textAlign: "right" as const }}>${p.projected_monthly_cost.toFixed(0)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function FormField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div style={{ fontSize: "11px", color: "var(--color-text-muted)", fontFamily: "var(--font-mono)", textTransform: "uppercase" as const, letterSpacing: "0.06em", marginBottom: "6px" }}>{label}</div>
      {children}
    </div>
  );
}

function MetricBox({ label, value, highlight }: { label: string; value: string; highlight?: string }) {
  return (
    <div style={{ background: "var(--color-bg-elevated)", borderRadius: "8px", padding: "10px 12px" }}>
      <div style={{ fontSize: "10px", color: "var(--color-text-muted)", fontFamily: "var(--font-mono)", textTransform: "uppercase" as const, letterSpacing: "0.06em", marginBottom: "4px" }}>{label}</div>
      <div style={{ fontSize: "18px", fontFamily: "var(--font-mono)", fontWeight: 700, color: highlight ?? "var(--color-text-primary)" }}>{value}</div>
    </div>
  );
}

const inputStyle: React.CSSProperties = { padding: "8px 10px", background: "var(--color-bg-elevated)", border: "1px solid var(--color-border-subtle)", borderRadius: "6px", color: "var(--color-text-primary)", fontSize: "12px", fontFamily: "var(--font-mono)", outline: "none" };
const sectionLabel: React.CSSProperties = { fontSize: "11px", color: "var(--color-text-muted)", fontFamily: "var(--font-mono)", textTransform: "uppercase", letterSpacing: "0.08em" };

const MOCK_SIM_RESULT = {
  id: "sim-001",
  description: "Scale api-gateway from 3?5 replicas over 24h window at SLO 99.50%",
  original_cost: { monthly_usd: 1296 },
  simulated_cost: { monthly_usd: 1134 },
  cost_delta_percent: -12.5,
  slo_breaches: 0,
  total_steps: 144,
};

const MOCK_CURVE = {
  service: "api-gateway",
  points: [
    { slo_target: 0.9999, projected_monthly_cost: 2592, projected_compliance_percent: 99.99, avg_replicas: 9 },
    { slo_target: 0.999, projected_monthly_cost: 1944, projected_compliance_percent: 99.90, avg_replicas: 7 },
    { slo_target: 0.995, projected_monthly_cost: 1296, projected_compliance_percent: 99.50, avg_replicas: 5 },
    { slo_target: 0.99, projected_monthly_cost: 972, projected_compliance_percent: 99.00, avg_replicas: 4 },
    { slo_target: 0.98, projected_monthly_cost: 648, projected_compliance_percent: 98.00, avg_replicas: 3 },
  ],
};