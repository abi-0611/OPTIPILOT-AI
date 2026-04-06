import { NavLink, Outlet } from "react-router-dom";
import { Activity, Users, BookOpen, FlaskConical, Cpu } from "lucide-react";
import { useMeta } from "@/api/hooks";

const NAV = [
  { to: "/", label: "SLO Overview", icon: Activity },
  { to: "/fairness", label: "Fairness", icon: Users },
  { to: "/decisions", label: "Decisions", icon: BookOpen },
  { to: "/whatif", label: "What-If", icon: FlaskConical },
];

export default function Layout() {
  const meta = useMeta();
  const cluster = meta.data?.cluster_name ?? (meta.isLoading ? "…" : "unknown");
  const apiOk = meta.isSuccess;

  return (
    <div style={{ display: "flex", height: "100vh", overflow: "hidden", background: "var(--color-bg-base)" }}>
      {/* Sidebar */}
      <aside style={{
        width: "220px", flexShrink: 0,
        background: "var(--color-bg-surface)",
        borderRight: "1px solid var(--color-border-default)",
        display: "flex", flexDirection: "column",
        padding: "0",
      }}>
        {/* Logo */}
        <div style={{
          padding: "20px 20px 16px",
          borderBottom: "1px solid var(--color-border-subtle)",
        }}>
          <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
            <div style={{
              width: "32px", height: "32px", borderRadius: "8px",
              background: "linear-gradient(135deg, var(--color-cyan-glow), #0ea5e9)",
              display: "flex", alignItems: "center", justifyContent: "center",
              boxShadow: "var(--shadow-glow-cyan)",
            }}>
              <Cpu size={16} color="#080d12" strokeWidth={2.5} />
            </div>
            <div>
              <div style={{ fontFamily: "var(--font-display)", fontWeight: 700, fontSize: "15px", color: "var(--color-text-primary)", lineHeight: 1 }}>OptiPilot</div>
              <div style={{ fontSize: "10px", color: "var(--color-text-muted)", fontFamily: "var(--font-mono)", letterSpacing: "0.08em", marginTop: "2px" }}>AI</div>
            </div>
          </div>
        </div>

        {/* Cluster selector */}
        <div style={{ padding: "12px 16px", borderBottom: "1px solid var(--color-border-subtle)" }}>
          <div style={{ fontSize: "10px", color: "var(--color-text-muted)", textTransform: "uppercase", letterSpacing: "0.1em", marginBottom: "6px", fontFamily: "var(--font-mono)" }}>Cluster</div>
          <select
            disabled
            value={cluster}
            style={{
              width: "100%", background: "var(--color-bg-elevated)",
              border: "1px solid var(--color-border-default)",
              color: "var(--color-text-secondary)", borderRadius: "6px",
              padding: "6px 8px", fontSize: "12px", fontFamily: "var(--font-mono)", outline: "none",
              opacity: 0.95,
            }}
          >
            <option value={cluster}>{cluster}</option>
          </select>
        </div>

        {/* Nav */}
        <nav style={{ padding: "8px 8px", flex: 1 }}>
          {NAV.map(({ to, label, icon: Icon }) => (
            <NavLink
              key={to} to={to} end={to === "/"}
              style={({ isActive }) => ({
                display: "flex", alignItems: "center", gap: "10px",
                padding: "9px 12px", borderRadius: "7px", marginBottom: "2px",
                textDecoration: "none",
                fontSize: "13px",
                fontWeight: isActive ? 600 : 400,
                color: isActive ? "var(--color-cyan-glow)" : "var(--color-text-secondary)",
                background: isActive ? "rgba(34,211,238,0.07)" : "transparent",
                transition: "all 0.15s ease",
              })}
            >
              {({ isActive }) => (
                <>
                  <Icon size={15} strokeWidth={isActive ? 2.5 : 2} />
                  {label}
                </>
              )}
            </NavLink>
          ))}
        </nav>

        {/* Footer */}
        <div style={{ padding: "12px 16px", borderTop: "1px solid var(--color-border-subtle)" }}>
          <div style={{ fontSize: "10px", color: "var(--color-text-muted)", fontFamily: "var(--font-mono)" }}>v0.9.0-alpha</div>
        </div>
      </aside>

      {/* Main */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden" }}>
        {/* Header */}
        <header style={{
          height: "52px", flexShrink: 0,
          background: "var(--color-bg-surface)",
          borderBottom: "1px solid var(--color-border-default)",
          display: "flex", alignItems: "center",
          padding: "0 24px",
          gap: "12px",
        }}>
          <div style={{ flex: 1 }} />
          <StatusPill label="API" ok={apiOk} />
        </header>

        {/* Page content */}
        <main style={{ flex: 1, overflow: "auto", padding: "24px" }}>
          <Outlet />
        </main>
      </div>
    </div>
  );
}

function StatusPill({ label, ok }: { label: string; ok: boolean }) {
  return (
    <div style={{
      display: "flex", alignItems: "center", gap: "5px",
      padding: "3px 8px", borderRadius: "20px",
      background: ok ? "rgba(16,185,129,0.1)" : "rgba(244,63,94,0.1)",
      border: `1px solid ${ok ? "rgba(16,185,129,0.3)" : "rgba(244,63,94,0.3)"}`,
      fontSize: "11px", fontFamily: "var(--font-mono)", color: ok ? "var(--color-emerald)" : "var(--color-rose)",
    }}>
      <div style={{ width: "5px", height: "5px", borderRadius: "50%", background: ok ? "var(--color-emerald)" : "var(--color-rose)", boxShadow: ok ? "0 0 6px var(--color-emerald)" : "0 0 6px var(--color-rose)" }} />
      {label}
    </div>
  );
}