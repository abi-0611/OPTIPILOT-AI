import { useState } from "react";
import { useDecisions, useDecisionExplain } from "@/api/hooks";
import type { DecisionRecord } from "@/api/types";
import { formatScalingSummary } from "@/lib/utils";
import { Search, ChevronDown, ChevronRight } from "lucide-react";

export default function DecisionExplorer() {
  const [search, setSearch] = useState("");
  const [trigger, setTrigger] = useState("");
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const decisions = useDecisions({ trigger: trigger || undefined, limit: 50 });
  const data = decisions.data ?? [];
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
        {decisions.isLoading ? "…" : `${filtered.length} decision${filtered.length !== 1 ? "s" : ""}`}
      </div>

      {decisions.isError && (
        <div style={{ marginBottom: "12px", color: "var(--color-rose)", fontSize: "13px" }}>{(decisions.error as Error).message}</div>
      )}

      {/* Timeline */}
      <div style={{ display: "flex", flexDirection: "column", gap: "4px" }}>
        {decisions.isLoading && <div style={{ color: "var(--color-text-muted)", fontSize: "13px" }}>Loading decisions…</div>}
        {!decisions.isLoading && !decisions.isError && filtered.length === 0 && (
          <div style={{ color: "var(--color-text-muted)", fontSize: "13px", padding: "16px 0" }}>
            No decisions in the journal yet. The optimizer will record actions here as it runs.
          </div>
        )}
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

function DecisionCard({ record, expanded, onToggle }: { record: DecisionRecord; expanded: boolean; onToggle: () => void }) {
  const explain = useDecisionExplain(expanded ? record.id : "");

  const actionColor: Record<string, string> = {
    scale_up: "var(--color-cyan-glow)", scale_down: "var(--color-amber)",
    no_action: "var(--color-text-muted)", tune: "var(--color-sky)",
  };
  const color = actionColor[record.actionType] ?? "var(--color-text-secondary)";

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
          {(record.actionType ?? "").replace(/_/g, " ") || "—"}
        </span>
        <span style={{ fontFamily: "var(--font-mono)", fontSize: "11px", color: "var(--color-text-muted)", minWidth: "120px", textAlign: "right" as const }}>
          {formatScalingSummary(record) ?? "—"}
        </span>
        <ConfPct value={record.confidence} />
        {record.dryRun && <DryCapsule />}
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
                  {explain.data?.narrative ?? record.selectedAction?.reason}
                </div>
            }
          </div>

          {/* Trade-off radar placeholder */}
          <div style={{ marginBottom: "12px" }}>
            <Label text="Objective Weights" />
            <div style={{ display: "flex", gap: "8px", flexWrap: "wrap" as const }}>
              {Object.entries(record.objectiveWeights ?? {}).map(([k, v]) => (
                <WeightPill key={k} name={k} value={v as number} />
              ))}
            </div>
          </div>

          {/* Candidates mini-table */}
          {record.candidates && record.candidates.length > 0 && (
            <div>
              <Label text={`Candidates (${record.candidates.length})`} />
              <div style={{ display: "flex", gap: "6px", flexWrap: "wrap" as const }}>
                {record.candidates.map((c, i) => {
                  const cost = c.plan.estimated_cost ?? c.plan.EstimatedCost;
                  return (
                  <div key={i} style={{ fontSize: "11px", fontFamily: "var(--font-mono)", color: "var(--color-text-muted)", background: "var(--color-bg-overlay)", padding: "4px 8px", borderRadius: "4px" }}>
                    {c.plan.replicas} rep · est ${cost !== undefined ? cost.toFixed(2) : "—"}/h
                  </div>
                  );
                })}
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