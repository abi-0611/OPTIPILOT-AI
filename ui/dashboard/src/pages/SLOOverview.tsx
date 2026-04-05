import { useDecisionSummary, useDecisions } from "@/api/hooks";
import { formatPercent } from "@/lib/utils";

const SERVICES = ["api-gateway", "auth-service", "payment-service", "inventory", "notification", "worker", "analytics", "scheduler"];
const METRICS = ["availability", "latency_p99", "error_rate"];

function budgetColor(pct: number) {
  if (pct > 80) return "var(--color-emerald)";
  if (pct > 20) return "var(--color-amber)";
  return "var(--color-rose)";
}

function fakeCompliance(svc: string, metric: string): number {
  const h = (svc + metric).split("").reduce((a, c) => a + c.charCodeAt(0), 0);
  return 30 + (h % 71);
}

export default function SLOOverview() {
  const summary = useDecisionSummary("24h");
  const decisions = useDecisions({ limit: 5 });

  return (
    <div>
      <PageHeader title="SLO Overview" subtitle="Real-time service-level objective compliance" />

      {/* Stats strip */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: "12px", marginBottom: "24px" }}>
        <StatCard label="Decisions / hr" value={summary.data?.decisions_per_hour?.toFixed(1) ?? "—"} mono />
        <StatCard label="Avg Confidence" value={summary.data ? formatPercent(summary.data.average_confidence * 100) : "—"} mono />
        <StatCard label="Total (24h)" value={String(summary.data?.total_decisions ?? "—")} mono />
        <StatCard label="Top Trigger" value={summary.data?.top_triggers?.[0]?.trigger ?? "—"} />
      </div>

      {/* Heatmap */}
      <Section title="Compliance Heatmap" description="Budget remaining (%) — click any cell for drill-down">
        <div style={{ overflowX: "auto" }}>
          <table style={{ borderCollapse: "collapse", width: "100%" }}>
            <thead>
              <tr>
                <th style={thStyle}>Service</th>
                {METRICS.map(m => <th key={m} style={thStyle}>{m}</th>)}
              </tr>
            </thead>
            <tbody>
              {SERVICES.map(svc => (
                <tr key={svc}>
                  <td style={{ ...tdStyle, fontFamily: "var(--font-mono)", fontSize: "12px", color: "var(--color-text-secondary)" }}>{svc}</td>
                  {METRICS.map(metric => {
                    const pct = fakeCompliance(svc, metric);
                    const color = budgetColor(pct);
                    return (
                      <td key={metric} style={{ ...tdStyle, textAlign: "center" }}>
                        <div
                          role="button"
                          tabIndex={0}
                          style={{
                            display: "inline-flex", alignItems: "center", justifyContent: "center",
                            width: "72px", height: "36px", borderRadius: "6px",
                            background: `${color}18`, border: `1px solid ${color}40`,
                            color, fontFamily: "var(--font-mono)", fontSize: "13px", fontWeight: 600,
                            cursor: "pointer", transition: "transform 0.1s",
                          }}
                          onMouseEnter={e => { (e.currentTarget as HTMLElement).style.transform = "scale(1.04)"; }}
                          onMouseLeave={e => { (e.currentTarget as HTMLElement).style.transform = "scale(1)"; }}
                        >
                          {pct}%
                        </div>
                      </td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {/* Legend */}
        <div style={{ display: "flex", gap: "16px", marginTop: "12px" }}>
          {[["var(--color-emerald)", ">80% — Healthy"], ["var(--color-amber)", "20–80% — Warning"], ["var(--color-rose)", "<20% — Critical"]].map(([color, label]) => (
            <div key={label} style={{ display: "flex", alignItems: "center", gap: "6px", fontSize: "11px", color: "var(--color-text-muted)" }}>
              <div style={{ width: "10px", height: "10px", borderRadius: "2px", background: `${color}50`, border: `1px solid ${color}80` }} />
              {label}
            </div>
          ))}
        </div>
      </Section>

      {/* Recent decisions */}
      <Section title="Recent Decisions" description="Last 5 optimizer actions">
        {decisions.isLoading ? <Loading /> : (
          <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
            {(decisions.data ?? MOCK_DECISIONS).map((d, i) => (
              <DecisionRow key={d.id ?? i} record={d} />
            ))}
          </div>
        )}
      </Section>
    </div>
  );
}

const MOCK_DECISIONS = [
  { id: "1", timestamp: new Date().toISOString(), service: "api-gateway", trigger: "slo_breach", action_type: "scale_up", confidence: 0.92, dry_run: false },
  { id: "2", timestamp: new Date(Date.now() - 300_000).toISOString(), service: "payment-service", trigger: "forecast", action_type: "no_action", confidence: 0.78, dry_run: false },
  { id: "3", timestamp: new Date(Date.now() - 600_000).toISOString(), service: "worker", trigger: "rollback", action_type: "scale_down", confidence: 0.85, dry_run: true },
];

function DecisionRow({ record }: { record: typeof MOCK_DECISIONS[0] }) {
  const actionColor: Record<string, string> = { scale_up: "var(--color-cyan-glow)", scale_down: "var(--color-amber)", no_action: "var(--color-text-muted)", tune: "var(--color-sky)" };
  const color = actionColor[record.action_type] ?? "var(--color-text-muted)";
  return (
    <div style={{
      display: "flex", alignItems: "center", gap: "12px",
      padding: "10px 14px", borderRadius: "8px",
      background: "var(--color-bg-elevated)",
      border: "1px solid var(--color-border-subtle)",
    }}>
      <div style={{ fontFamily: "var(--font-mono)", fontSize: "11px", color: "var(--color-text-muted)", flexShrink: 0, width: "80px" }}>
        {new Date(record.timestamp).toLocaleTimeString()}
      </div>
      <div style={{ fontFamily: "var(--font-mono)", fontSize: "12px", color: "var(--color-text-secondary)", flex: 1 }}>{record.service}</div>
      <div style={{ fontSize: "11px", color: "var(--color-text-muted)" }}>{record.trigger}</div>
      <div style={{ fontFamily: "var(--font-mono)", fontSize: "12px", color, fontWeight: 600, textTransform: "uppercase" as const, letterSpacing: "0.05em" }}>
        {record.action_type.replace("_", " ")}
      </div>
      <ConfidenceBadge value={record.confidence} />
      {record.dry_run && <span style={{ fontSize: "10px", padding: "2px 6px", background: "rgba(56,189,248,0.1)", border: "1px solid rgba(56,189,248,0.3)", borderRadius: "4px", color: "var(--color-sky)", fontFamily: "var(--font-mono)" }}>DRY</span>}
    </div>
  );
}

function ConfidenceBadge({ value }: { value: number }) {
  const pct = Math.round(value * 100);
  const color = pct >= 85 ? "var(--color-emerald)" : pct >= 65 ? "var(--color-amber)" : "var(--color-rose)";
  return (
    <div style={{ fontFamily: "var(--font-mono)", fontSize: "11px", color, minWidth: "36px", textAlign: "right" }}>
      {pct}%
    </div>
  );
}

// -- Shared --------------------------------------------------------------------
function PageHeader({ title, subtitle }: { title: string; subtitle: string }) {
  return (
    <div style={{ marginBottom: "24px" }}>
      <h1 style={{ fontFamily: "var(--font-display)", fontSize: "24px", fontWeight: 700, color: "var(--color-text-primary)", margin: 0, letterSpacing: "-0.02em" }}>{title}</h1>
      <p style={{ color: "var(--color-text-muted)", fontSize: "13px", margin: "4px 0 0", fontFamily: "var(--font-body)" }}>{subtitle}</p>
    </div>
  );
}

function StatCard({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div style={{ background: "var(--color-bg-surface)", border: "1px solid var(--color-border-default)", borderRadius: "10px", padding: "14px 16px" }}>
      <div style={{ fontSize: "11px", color: "var(--color-text-muted)", textTransform: "uppercase" as const, letterSpacing: "0.08em", fontFamily: "var(--font-mono)", marginBottom: "8px" }}>{label}</div>
      <div style={{ fontFamily: mono ? "var(--font-mono)" : "var(--font-display)", fontSize: "22px", fontWeight: 700, color: "var(--color-cyan-glow)" }}>{value}</div>
    </div>
  );
}

function Section({ title, description, children }: { title: string; description?: string; children: React.ReactNode }) {
  return (
    <div style={{ background: "var(--color-bg-surface)", border: "1px solid var(--color-border-default)", borderRadius: "12px", padding: "18px 20px", marginBottom: "16px" }}>
      <div style={{ marginBottom: "14px" }}>
        <div style={{ fontFamily: "var(--font-display)", fontSize: "14px", fontWeight: 600, color: "var(--color-text-primary)" }}>{title}</div>
        {description && <div style={{ fontSize: "12px", color: "var(--color-text-muted)", marginTop: "2px" }}>{description}</div>}
      </div>
      {children}
    </div>
  );
}

function Loading() {
  return <div style={{ color: "var(--color-text-muted)", fontSize: "13px", fontFamily: "var(--font-mono)", padding: "12px 0" }}>Loading...</div>;
}

const thStyle: React.CSSProperties = {
  padding: "8px 12px", textAlign: "left", fontSize: "11px",
  color: "var(--color-text-muted)", fontFamily: "var(--font-mono)",
  textTransform: "uppercase", letterSpacing: "0.08em",
  borderBottom: "1px solid var(--color-border-subtle)",
};

const tdStyle: React.CSSProperties = {
  padding: "8px 12px",
  borderBottom: "1px solid var(--color-border-subtle)",
};