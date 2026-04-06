import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import axe from "axe-core";
import { renderWithProviders } from "../test/test-utils";
import FairnessDashboard from "./FairnessDashboard";

vi.mock("@/api/hooks", () => ({
  useTenants: () => ({ data: [], isLoading: false, isError: false }),
  useFairness: () => ({ data: { timestamp: "", global_index: 0, per_tenant: {} }, isLoading: false }),
}));

describe("FairnessDashboard", () => {
  it("renders the page heading", () => {
    renderWithProviders(<FairnessDashboard />);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Fairness Dashboard");
  });

  it("renders the three summary stat cards", () => {
    renderWithProviders(<FairnessDashboard />);
    expect(screen.getByText(/global fairness index/i)).toBeInTheDocument();
    expect(screen.getByText(/active tenants/i)).toBeInTheDocument();
    expect(screen.getByText(/noisy alerts/i)).toBeInTheDocument();
  });

  it("shows empty state when no tenants are configured", () => {
    renderWithProviders(<FairnessDashboard />);
    expect(screen.getByText(/no tenant state yet/i)).toBeInTheDocument();
  });

  it("has no serious or critical accessibility violations", async () => {
    const { container } = renderWithProviders(<FairnessDashboard />);
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
