import { useState } from "react";
import { useDecisions, useDecisionExplain } from "@/api/hooks";
import { Search, ChevronDown, ChevronRight } from "lucide-react";

type DecisionEntry = {
  id: string;
  timestamp: string;
  service: string;
  trigger: string;
  action_type: string;
  confidence: number;
  dry_run: boolean;
  narrative: string;
  weights?: Record<string, number> | null;
  candidates?: { replicas: number; estimatedCost: number }[] | null;
};

export default function DecisionExplorer() {
  const [search, setSearch] = useState("");
  const [trigger, setTrigger] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const decisions = useDecisions({ trigger: trigger || undefined, limit: 50 });
  const data = (decisions.data as DecisionEntry[] | undefined) ?? MOCK_DECISIONS;
  const filtered = search ? data.filter(d => d.service.includes(search) || d.trigger.includes(search)) : data;

  return (
    <div>
      <div style={{ marginBottom: "24px" }}>
        <h1 style={{ fontFamily: "var(--font-display)", fontSize: "24px", fontWeight: 700, color: "var(--color-text-primary)", margin: 0, letterSpacing: "-0.02em" }}>Decision Explorer</h1>
        <p style={{ color: "var(--color-text-muted)", fontSize: "13px", margin: "4px 0 0" }}>Filterable optimizer decision timeline with full narrative drill-down</p>
      </div>

      {/* Filter bar */}
      <div style={{ display: "flex", gap: "10px", marginBottom: "16px" }}>
        <div style={{ flex: 1, position: "relative" as const }}>
          <Search size={14} style={{ position: "absolute" as const, left: "12px", top: "50%", transform: "translateY(-50%)", color: "var(--color-text-muted)" }} />
          <input
            aria-label="Search decisions by service or trigger"
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="Search service or trigger..."
            style={{
              width: "100%", padding: "9px 12px 9px 34px",
              background: "var(--color-bg-surface)", border: "1px solid var(--color-border-default)",
              borderRadius: "8px", color: "var(--color-text-primary)", fontSize: "13px",
              fontFamily: "var(--font-body)", outline: "none",
              boxSizing: "border-box" as const,
            }}
          />
        </div>
        <select
          aria-label="Filter by trigger type"
          value={trigger}
          onChange={e => setTrigger(e.target.value)}
          style={{ padding: "9px 12px", background: "var(--color-bg-surface)", border: "1px solid var(--color-border-default)", borderRadius: "8px", color: "var(--color-text-secondary)", fontSize: "13px", fontFamily: "var(--font-mono)", outline: "none" }}
        >
          <option value="">All triggers</option>
          {["slo_breach", "forecast", "rollback", "scale", "tune"].map(t => <option key={t} value={t}>{t}</option>)}
        </select>
      </div>

      {/* Count */}
      <div style={{ fontSize: "12px", color: "var(--color-text-muted)", fontFamily: "var(--font-mono)", marginBottom: "10px" }}>
        {filtered.length} decision{filtered.length !== 1 ? "s" : ""}
      </div>

      {/* Timeline */}
      <div style={{ display: "flex", flexDirection: "column", gap: "4px" }}>
        {filtered.map((d, i) => (
          <DecisionCard
            key={d.id ?? i}
            record={d}
            expanded={expandedId === d.id}
            onToggle={() => setExpandedId(expandedId === d.id ? null : d.id)}
          />
        ))}
      </div>
    </div>
  );
}

function DecisionCard({ record, expanded, onToggle }: { record: DecisionEntry; expanded: boolean; onToggle: () => void }) {
  const explain = useDecisionExplain(expanded ? record.id : "");

  const actionColor: Record<string, string> = {
    scale_up: "var(--color-cyan-glow)", scale_down: "var(--color-amber)",
    no_action: "var(--color-text-muted)", tune: "var(--color-sky)",
  };
  const color = actionColor[record.action_type] ?? "var(--color-text-secondary)";

  return (
    <div style={{
      background: "var(--color-bg-surface)", border: `1px solid ${expanded ? "var(--color-border-default)" : "var(--color-border-subtle)"}`,
      borderRadius: "10px", overflow: "hidden",
      transition: "border-color 0.15s",
    }}>
      {/* Row */}
      <div
        role="button"
        tabIndex={0}
        onClick={onToggle}
        style={{ display: "flex", alignItems: "center", gap: "12px", padding: "11px 14px", cursor: "pointer" }}
      >
        <span style={{ color: "var(--color-text-muted)", flexShrink: 0 }}>{expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}</span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: "11px", color: "var(--color-text-muted)", width: "76px", flexShrink: 0 }}>{new Date(record.timestamp).toLocaleTimeString()}</span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: "12px", color: "var(--color-text-secondary)", flex: 1 }}>{record.service}</span>
        <span style={{ fontSize: "12px", color: "var(--color-text-muted)", minWidth: "80px" }}>{record.trigger}</span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: "12px", color, fontWeight: 600, textTransform: "uppercase" as const, letterSpacing: "0.05em", minWidth: "88px", textAlign: "right" as const }}>
          {record.action_type.replace("_", " ")}
        </span>
        <ConfPct value={record.confidence} />
        {record.dry_run && <DryCapsule />}
      </div>

      {/* Expanded detail */}
      {expanded && (
        <div style={{ borderTop: "1px solid var(--color-border-subtle)", padding: "14px 16px", background: "var(--color-bg-elevated)" }}>
          {/* Narrative */}
          <div style={{ marginBottom: "14px" }}>
            <Label text="Narrative" />
            {explain.isLoading
              ? <div style={monoText}>Loading explanation...</div>
              : <div style={{ fontSize: "13px", color: "var(--color-text-secondary)", lineHeight: 1.6, fontFamily: "var(--font-body)" }}>
                  {explain.data?.narrative ?? record.narrative}
                </div>
            }
          </div>

          {/* Trade-off radar placeholder */}
          <div style={{ marginBottom: "12px" }}>
            <Label text="Objective Weights" />
            <div style={{ display: "flex", gap: "8px", flexWrap: "wrap" as const }}>
              {Object.entries(record.weights ?? {}).map(([k, v]) => (
                <WeightPill key={k} name={k} value={v as number} />
              ))}
            </div>
          </div>

          {/* Candidates mini-table */}
          {record.candidates && (
            <div>
              <Label text={`Candidates (${record.candidates.length})`} />
              <div style={{ display: "flex", gap: "6px", flexWrap: "wrap" as const }}>
                {record.candidates.map((c, i) => (
                  <div key={i} style={{ fontSize: "11px", fontFamily: "var(--font-mono)", color: "var(--color-text-muted)", background: "var(--color-bg-overlay)", padding: "4px 8px", borderRadius: "4px" }}>
                    {c.replicas}r � ${c.estimatedCost}/h
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function ConfPct({ value }: { value: number }) {
  const pct = Math.round(value * 100);
  const c = pct >= 85 ? "var(--color-emerald)" : pct >= 65 ? "var(--color-amber)" : "var(--color-rose)";
  return <span style={{ fontFamily: "var(--font-mono)", fontSize: "11px", color: c, width: "32px", textAlign: "right" as const }}>{pct}%</span>;
}

function DryCapsule() {
  return <span style={{ fontSize: "10px", padding: "2px 5px", background: "rgba(56,189,248,0.1)", border: "1px solid rgba(56,189,248,0.3)", borderRadius: "3px", color: "var(--color-sky)", fontFamily: "var(--font-mono)" }}>DRY</span>;
}

function Label({ text }: { text: string }) {
  return <div style={{ fontSize: "10px", color: "var(--color-text-muted)", textTransform: "uppercase" as const, letterSpacing: "0.08em", fontFamily: "var(--font-mono)", marginBottom: "6px" }}>{text}</div>;
}

function WeightPill({ name, value }: { name: string; value: number }) {
  const pct = Math.round(value * 100);
  const c: Record<string, string> = { slo: "var(--color-cyan-glow)", cost: "var(--color-amber)", carbon: "var(--color-emerald)", fairness: "var(--color-sky)" };
  const color = c[name] ?? "var(--color-text-muted)";
  return (
    <div style={{ display: "flex", alignItems: "center", gap: "5px", fontSize: "11px", fontFamily: "var(--font-mono)", color }}>
      <div style={{ width: "28px", height: "4px", borderRadius: "2px", background: `${color}30` }}>
        <div style={{ height: "100%", width: `${pct}%`, background: color, borderRadius: "2px" }} />
      </div>
      {name} {pct}%
    </div>
  );
}

const monoText: React.CSSProperties = { fontFamily: "var(--font-mono)", fontSize: "12px", color: "var(--color-text-muted)" };

const MOCK_DECISIONS = [
  { id: "d-001", timestamp: new Date().toISOString(), service: "api-gateway", trigger: "slo_breach", action_type: "scale_up", confidence: 0.92, dry_run: false, narrative: "At 14:32 UTC, api-gateway was scaled from 3?5 replicas due to SLO breach. Burn rate 2.4� (budget: 18% remaining). Forecasted +34% RPS in next 15 min with 87% confidence. Selected plan: 5 replicas at $1.80/h (spot 60%). Confidence: 92%.", weights: { slo: 0.5, cost: 0.3, carbon: 0.1, fairness: 0.1 }, candidates: [{ replicas: 4, estimatedCost: 1.44 }, { replicas: 5, estimatedCost: 1.80 }, { replicas: 6, estimatedCost: 2.16 }] },
  { id: "d-002", timestamp: new Date(Date.now() - 5 * 60000).toISOString(), service: "payment-service", trigger: "forecast", action_type: "no_action", confidence: 0.78, dry_run: false, narrative: "SLO compliant (budget 72% remaining, burn rate 0.6�). No traffic surge forecasted. Current 4 replicas sufficient. No action taken.", weights: { slo: 0.4, cost: 0.4, carbon: 0.1, fairness: 0.1 }, candidates: null },
  { id: "d-003", timestamp: new Date(Date.now() - 12 * 60000).toISOString(), service: "worker", trigger: "rollback", action_type: "scale_down", confidence: 0.85, dry_run: false, narrative: "Rollback triggered: previous scale-up degraded latency p99 from 180ms?310ms. Reverted to 3 replicas. SLO recovering.", weights: { slo: 0.6, cost: 0.2, carbon: 0.1, fairness: 0.1 }, candidates: [{ replicas: 3, estimatedCost: 1.08 }, { replicas: 4, estimatedCost: 1.44 }] },
  { id: "d-004", timestamp: new Date(Date.now() - 20 * 60000).toISOString(), service: "inventory", trigger: "slo_breach", action_type: "tune", confidence: 0.71, dry_run: true, narrative: "(DRY RUN) Would tune connection pool from 50?80 and GC threshold from 512?768MiB. Projected latency improvement: -22ms p99.", weights: { slo: 0.3, cost: 0.4, carbon: 0.2, fairness: 0.1 }, candidates: null },
  { id: "d-005", timestamp: new Date(Date.now() - 35 * 60000).toISOString(), service: "analytics", trigger: "scale", action_type: "scale_up", confidence: 0.94, dry_run: false, narrative: "Scheduled scale-up for peak analytics window (18:00�20:00 UTC). Pre-warmed 2?4 replicas. Forecast confidence 94%.", weights: { slo: 0.5, cost: 0.3, carbon: 0.1, fairness: 0.1 }, candidates: [{ replicas: 3, estimatedCost: 1.20 }, { replicas: 4, estimatedCost: 1.60 }] },
];