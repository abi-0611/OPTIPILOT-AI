import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { renderWithProviders } from "../test/test-utils";
import WhatIfTool from "./WhatIfTool";

const mockSimMutate = vi.fn();
const mockCurveMutate = vi.fn();

vi.mock("@/api/hooks", () => ({
  useRunSimulation: () => ({ mutate: mockSimMutate, isPending: false, data: undefined }),
  useSLOCostCurve: () => ({ mutate: mockCurveMutate, isPending: false, data: undefined }),
  useServiceObjectives: () => ({
    data: [
      {
        namespace: "demo",
        name: "x-slo",
        targetName: "api-gateway",
        targetKind: "Deployment",
        objectives: [{ metric: "availability", target: "99.9%" }],
      },
    ],
    isLoading: false,
  }),
}));

describe("WhatIfTool", () => {
  it("renders the page heading", () => {
    renderWithProviders(<WhatIfTool />);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("What-If Simulator");
  });

  it("renders service selector defaulting to first workload from ServiceObjectives", () => {
    renderWithProviders(<WhatIfTool />);
    const select = screen.getByRole("combobox", { name: /service/i });
    expect(select).toHaveValue("api-gateway");
  });

  it("renders SLO target range slider", () => {
    renderWithProviders(<WhatIfTool />);
    expect(screen.getByRole("slider", { name: /slo sweep center/i })).toBeInTheDocument();
  });

  it("renders Run Simulation and Generate SLO-Cost Curve buttons", () => {
    renderWithProviders(<WhatIfTool />);
    expect(screen.getByRole("button", { name: /run simulation/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /generate slo-cost curve/i })).toBeInTheDocument();
  });

  it("calls sim.mutate with API-shaped body when form is submitted", async () => {
    const user = userEvent.setup();
    renderWithProviders(<WhatIfTool />);
    await user.click(screen.getByRole("button", { name: /run simulation/i }));
    expect(mockSimMutate).toHaveBeenCalledOnce();
    const [args] = mockSimMutate.mock.calls[0];
    expect(args).toMatchObject({
      services: ["api-gateway"],
      step: 5 * 60 * 1e9,
    });
    expect(typeof args.start).toBe("string");
    expect(typeof args.end).toBe("string");
  });

  it("shows empty state until a simulation succeeds", () => {
    renderWithProviders(<WhatIfTool />);
    expect(screen.getByText(/run a simulation or generate a curve/i)).toBeInTheDocument();
  });

  it("has no serious or critical accessibility violations", async () => {
    const { container } = renderWithProviders(<WhatIfTool />);
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
