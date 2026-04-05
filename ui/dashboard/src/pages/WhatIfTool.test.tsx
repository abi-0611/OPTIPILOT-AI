import { describe, it, expect, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import { renderWithProviders } from '../test/test-utils'
import WhatIfTool from './WhatIfTool'

const mockSimMutate = vi.fn()
const mockCurveMutate = vi.fn()

vi.mock('@/api/hooks', () => ({
  useRunSimulation: () => ({ mutate: mockSimMutate, isPending: false, data: undefined }),
  useSLOCostCurve: () => ({ mutate: mockCurveMutate, isPending: false, data: undefined }),
}))

describe('WhatIfTool', () => {
  it('renders the page heading', () => {
    renderWithProviders(<WhatIfTool />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('What-If Simulator')
  })

  it('renders service selector with api-gateway as default', () => {
    renderWithProviders(<WhatIfTool />)
    const select = screen.getByRole('combobox', { name: /service/i })
    expect(select).toBeInTheDocument()
    expect(select).toHaveValue('api-gateway')
  })

  it('renders SLO target range slider', () => {
    renderWithProviders(<WhatIfTool />)
    expect(screen.getByRole('slider', { name: /slo target/i })).toBeInTheDocument()
  })

  it('renders Run Simulation and Generate SLO-Cost Curve buttons', () => {
    renderWithProviders(<WhatIfTool />)
    expect(screen.getByRole('button', { name: /run simulation/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /generate slo-cost curve/i })).toBeInTheDocument()
  })

  it('calls sim.mutate with correct params when form is submitted', async () => {
    const user = userEvent.setup()
    renderWithProviders(<WhatIfTool />)
    await user.click(screen.getByRole('button', { name: /run simulation/i }))
    expect(mockSimMutate).toHaveBeenCalledOnce()
    const [args] = mockSimMutate.mock.calls[0]
    expect(args).toMatchObject({
      service: 'api-gateway',
      slo_target: expect.any(Number),
      dry_run: expect.any(Boolean),
    })
  })

  it('shows mock simulation result card by default', () => {
    renderWithProviders(<WhatIfTool />)
    // WhatIfTool renders MOCK_SIM_RESULT when sim.data is falsy
    expect(screen.getByText(/original cost/i)).toBeInTheDocument()
  })

  it('has no serious or critical accessibility violations', async () => {
    const { container } = renderWithProviders(<WhatIfTool />)
    const results = await axe.run(container, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] },
      rules: {
        'color-contrast': { enabled: false },
        'html-has-lang': { enabled: false },
      },
    })
    const blocking = results.violations.filter(
      v => v.impact === 'critical' || v.impact === 'serious',
    )
    expect(blocking, blocking.map(v => `${v.id}: ${v.description}`).join('\n')).toHaveLength(0)
  })
})
