import type { CSSProperties, ReactNode } from "react";
import { useDecisionSummary, useDecisions, useServiceObjectives } from "@/api/hooks";
import { formatForecastHint, formatPercent, formatScalingSummary } from "@/lib/utils";
import type { DecisionRecord, ServiceObjectiveSummary } from "@/api/types";

function collectMetricColumns(items: ServiceObjectiveSummary[]): string[] {
  const s = new Set<string>();
  for (const it of items) {
    for (const o of it.objectives) s.add(o.metric);
  }
  return [...s].sort();
}

function burnCellStyle(burn: number | undefined): { bg: string; border: string; color: string } {
  if (burn === undefined) {
    return {
      bg: "rgba(74,104,128,0.12)",
      border: "rgba(74,104,128,0.35)",
      color: "var(--color-text-muted)",
    };
  }
  if (burn <= 1) {
    return {
      bg: "rgba(16,185,129,0.12)",
      border: "rgba(16,185,129,0.4)",
      color: "var(--color-emerald)",
    };
  }
  if (burn <= 14) {
    return {
      bg: "rgba(245,158,11,0.12)",
      border: "rgba(245,158,11,0.4)",
      color: "var(--color-amber)",
    };
  }
  return {
    bg: "rgba(244,63,94,0.12)",
    border: "rgba(244,63,94,0.4)",
    color: "var(--color-rose)",
  };
}

export default function SLOOverview() {
  const summary = useDecisionSummary("24h");
  const decisions = useDecisions({ limit: 8 });
  const sloQuery = useServiceObjectives();

  const items = sloQuery.data ?? [];
  const metrics = collectMetricColumns(items);

  return (
    <div>
      <PageHeader
        title="SLO Overview"
        subtitle="Live ServiceObjective CRs from your cluster (status from OptiPilot evaluation)"
      />

      {sloQuery.isError && (
        <div style={{ marginBottom: "16px", padding: "12px 14px", borderRadius: "8px", background: "rgba(244,63,94,0.08)", border: "1px solid rgba(244,63,94,0.35)", color: "var(--color-rose)", fontSize: "13px" }}>
          Could not load ServiceObjectives: {(sloQuery.error as Error).message}
        </div>
      )}

      {/* Stats strip */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: "12px", marginBottom: "24px" }}>
        <StatCard label="Decisions / hr" value={summary.data?.decisions_per_hour?.toFixed(1) ?? "—"} mono />
        <StatCard label="Avg Confidence" value={summary.data ? formatPercent(summary.data.average_confidence * 100) : "—"} mono />
        <StatCard label="Total (24h)" value={String(summary.data?.total_decisions ?? "—")} mono />
        <StatCard label="Top Trigger" value={summary.data?.top_triggers?.[0]?.trigger ?? "—"} />
      </div>

      {/* Heatmap */}
      <Section
        title="SLO compliance heatmap"
        description="Per-objective burn rate (≤1× sustainable). Data from each ServiceObjective status."
      >
        {sloQuery.isLoading ? (
          <Loading />
        ) : items.length === 0 ? (
          <div style={{ color: "var(--color-text-muted)", fontSize: "13px", fontFamily: "var(--font-body)" }}>
            No ServiceObjective resources found. Apply a ServiceObjective CR for your workload to see live rows here.
          </div>
        ) : metrics.length === 0 ? (
          <div style={{ color: "var(--color-text-muted)", fontSize: "13px" }}>
            ServiceObjectives exist but list no objectives in spec yet.
          </div>
        ) : (
          <div style={{ overflowX: "auto" }}>
            <table style={{ borderCollapse: "collapse", width: "100%" }}>
              <thead>
                <tr>
                  <th style={thStyle}>Workload</th>
                  <th style={thStyle}>Budget %</th>
                  <th style={thStyle}>OK</th>
                  {metrics.map(m => (
                    <th key={m} style={thStyle}>
                      {m}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {items.map(so => (
                  <tr key={`${so.namespace}/${so.name}`}>
                    <td style={{ ...tdStyle, fontFamily: "var(--font-mono)", fontSize: "12px", color: "var(--color-text-secondary)" }}>
                      <div>{so.targetName}</div>
                      <div style={{ fontSize: "10px", color: "var(--color-text-muted)" }}>
                        {so.namespace}/{so.name}
                      </div>
                    </td>
                    <td style={tdStyle}>
                      <span style={{ fontFamily: "var(--font-mono)", fontSize: "12px", color: "var(--color-cyan-glow)" }}>
                        {so.budgetRemainingPercent !== undefined ? `${so.budgetRemainingPercent.toFixed(1)}%` : "—"}
                      </span>
                    </td>
                    <td style={tdStyle}>
                      {so.compliant === undefined ? "—" : so.compliant ? "✓" : "✗"}
                    </td>
                    {metrics.map(metric => {
                      const obj = so.objectives.find(o => o.metric === metric);
                      const br = obj?.burnRate ?? undefined;
                      const st = burnCellStyle(br);
                      return (
                        <td key={metric} style={{ ...tdStyle, textAlign: "center" }}>
                          <div
                            role="button"
                            tabIndex={0}
                            title={obj ? `Target ${obj.target}${obj.window ? ` (${obj.window})` : ""}` : undefined}
                            style={{
                              display: "inline-flex",
                              alignItems: "center",
                              justifyContent: "center",
                              minWidth: "72px",
                              height: "36px",
                              borderRadius: "6px",
                              background: st.bg,
                              border: `1px solid ${st.border}`,
                              color: st.color,
                              fontFamily: "var(--font-mono)",
                              fontSize: "12px",
                              fontWeight: 600,
                            }}
                          >
                            {br !== undefined ? `${br.toFixed(2)}×` : "—"}
                          </div>
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div style={{ display: "flex", gap: "16px", marginTop: "12px", flexWrap: "wrap" }}>
          {[
            ["var(--color-emerald)", "≤1× burn — healthy"],
            ["var(--color-amber)", "1–14× — investigate"],
            ["var(--color-rose)", ">14× — critical"],
            ["var(--color-text-muted)", "— — no burn data yet"],
          ].map(([color, label]) => (
            <div key={label} style={{ display: "flex", alignItems: "center", gap: "6px", fontSize: "11px", color: "var(--color-text-muted)" }}>
              <div style={{ width: "10px", height: "10px", borderRadius: "2px", background: `${color}50`, border: `1px solid ${color}80` }} />
              {label}
            </div>
          ))}
        </div>
      </Section>

      {/* Recent decisions */}
      <Section title="Recent decisions" description="Last decisions from the OptiPilot journal (live API)">
        {decisions.isLoading ? (
          <Loading />
        ) : decisions.isError ? (
          <div style={{ color: "var(--color-rose)", fontSize: "13px" }}>{(decisions.error as Error).message}</div>
        ) : !decisions.data?.length ? (
          <div style={{ color: "var(--color-text-muted)", fontSize: "13px" }}>
            No decisions recorded yet. When the optimizer acts on your workloads, entries appear here.
          </div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: "6px" }}>
            {decisions.data.map((d, i) => (
              <DecisionRow key={d.id ?? i} record={d} />
            ))}
          </div>
        )}
      </Section>
    </div>
  );
}

function DecisionRow({ record }: { record: DecisionRecord }) {
  const actionColor: Record<string, string> = {
    scale_up: "var(--color-cyan-glow)",
    scale_down: "var(--color-amber)",
    no_action: "var(--color-text-muted)",
    tune: "var(--color-sky)",
  };
  const color = actionColor[record.actionType] ?? "var(--color-text-muted)";
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: "12px",
        padding: "10px 14px",
        borderRadius: "8px",
        background: "var(--color-bg-elevated)",
        border: "1px solid var(--color-border-subtle)",
      }}
    >
      <div style={{ fontFamily: "var(--font-mono)", fontSize: "11px", color: "var(--color-text-muted)", flexShrink: 0, width: "80px" }}>
        {new Date(record.timestamp).toLocaleTimeString()}
      </div>
      <div style={{ fontFamily: "var(--font-mono)", fontSize: "12px", color: "var(--color-text-secondary)", flex: 1 }}>{record.service}</div>
      <div style={{ fontSize: "11px", color: "var(--color-text-muted)" }}>{record.trigger}</div>
      <div style={{ fontFamily: "var(--font-mono)", fontSize: "12px", color, fontWeight: 600, textTransform: "uppercase" as const, letterSpacing: "0.05em" }}>
        {(record.actionType ?? "").replace(/_/g, " ") || "—"}
      </div>
      <div style={{ display: "flex", flexDirection: "column", alignItems: "flex-end", gap: "2px", minWidth: "130px" }}>
        <div style={{ fontFamily: "var(--font-mono)", fontSize: "11px", color: "var(--color-text-muted)", textAlign: "right" as const }}>
          {formatScalingSummary(record) ?? "—"}
        </div>
        {formatForecastHint(record) && (
          <div style={{ fontFamily: "var(--font-mono)", fontSize: "10px", color: "var(--color-cyan-glow)", textAlign: "right" as const }}>
            {formatForecastHint(record)}
          </div>
        )}
      </div>
      <ConfidenceBadge value={record.confidence} />
      {record.dryRun && (
        <span
          style={{
            fontSize: "10px",
            padding: "2px 6px",
            background: "rgba(56,189,248,0.1)",
            border: "1px solid rgba(56,189,248,0.3)",
            borderRadius: "4px",
            color: "var(--color-sky)",
            fontFamily: "var(--font-mono)",
          }}
        >
          DRY
        </span>
      )}
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
      <div
        style={{
          fontSize: "11px",
          color: "var(--color-text-muted)",
          textTransform: "uppercase" as const,
          letterSpacing: "0.08em",
          fontFamily: "var(--font-mono)",
          marginBottom: "8px",
        }}
      >
        {label}
      </div>
      <div style={{ fontFamily: mono ? "var(--font-mono)" : "var(--font-display)", fontSize: "22px", fontWeight: 700, color: "var(--color-cyan-glow)" }}>{value}</div>
    </div>
  );
}

function Section({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
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

const thStyle: CSSProperties = {
  padding: "8px 12px",
  textAlign: "left",
  fontSize: "11px",
  color: "var(--color-text-muted)",
  fontFamily: "var(--font-mono)",
  textTransform: "uppercase",
  letterSpacing: "0.08em",
  borderBottom: "1px solid var(--color-border-subtle)",
};

const tdStyle: CSSProperties = {
  padding: "8px 12px",
  borderBottom: "1px solid var(--color-border-subtle)",
};
