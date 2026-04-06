import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { renderWithProviders } from "../test/test-utils";
import DecisionExplorer from "./DecisionExplorer";

const { mockDecisions } = vi.hoisted(() => ({
  mockDecisions: [
    { id: "d1", timestamp: "2026-01-01T12:00:00Z", service: "api-gateway", trigger: "slo_breach", actionType: "scale_up", confidence: 0.92, dryRun: false },
    { id: "d2", timestamp: "2026-01-01T11:00:00Z", service: "payment-service", trigger: "forecast", actionType: "no_action", confidence: 0.78, dryRun: false },
    { id: "d3", timestamp: "2026-01-01T10:00:00Z", service: "worker", trigger: "rollback", actionType: "scale_down", confidence: 0.85, dryRun: false },
    { id: "d4", timestamp: "2026-01-01T09:00:00Z", service: "inventory", trigger: "slo_breach", actionType: "tune", confidence: 0.71, dryRun: true },
    { id: "d5", timestamp: "2026-01-01T08:00:00Z", service: "analytics", trigger: "scale", actionType: "scale_up", confidence: 0.94, dryRun: false },
  ],
}));

vi.mock("@/api/hooks", () => ({
  useDecisions: () => ({ data: mockDecisions, isLoading: false }),
  useDecisionExplain: () => ({ data: { id: "d1", narrative: "Optimizer explanation text." }, isLoading: false }),
}));

describe("DecisionExplorer", () => {
  it("renders the page heading", () => {
    renderWithProviders(<DecisionExplorer />);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Decision Explorer");
  });

  it("shows the search input and trigger filter", () => {
    renderWithProviders(<DecisionExplorer />);
    expect(screen.getByRole("textbox", { name: /search decisions/i })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: /filter by trigger/i })).toBeInTheDocument();
  });

  it("renders decisions from the API and shows count", () => {
    renderWithProviders(<DecisionExplorer />);
    expect(screen.getByText("5 decisions")).toBeInTheDocument();
    expect(screen.getByText("api-gateway")).toBeInTheDocument();
    expect(screen.getByText("payment-service")).toBeInTheDocument();
    expect(screen.getByText("worker")).toBeInTheDocument();
  });

  it("filters decisions by typing a service name", async () => {
    const user = userEvent.setup();
    renderWithProviders(<DecisionExplorer />);
    const input = screen.getByRole("textbox", { name: /search decisions/i });
    await user.type(input, "worker");
    expect(screen.getByText("1 decision")).toBeInTheDocument();
    expect(screen.getByText("worker")).toBeInTheDocument();
    expect(screen.queryByText("api-gateway")).not.toBeInTheDocument();
  });

  it("expands a decision row and shows narrative section", async () => {
    const user = userEvent.setup();
    renderWithProviders(<DecisionExplorer />);
    const rows = screen.getAllByRole("button");
    await user.click(rows[0]);
    expect(screen.getByText("Narrative")).toBeInTheDocument();
  });

  it("has no serious or critical accessibility violations", async () => {
    const { container } = renderWithProviders(<DecisionExplorer />);
    const results = await axe.run(container, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa"] },
      rules: {
        "color-contrast": { enabled: false },
        "html-has-lang": { enabled: false },
      },
    });
    const blocking = results.violations.filter(v => v.impact === "critical" || v.impact === "serious");
    expect(blocking, blocking.map(v => `${v.id}: ${v.description}`).join("\n")).toHaveLength(0);
  });
});
