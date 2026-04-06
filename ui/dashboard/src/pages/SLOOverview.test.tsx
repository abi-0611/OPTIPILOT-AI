import { describe, it, expect, vi } from 'vitest'
import { screen } from '@testing-library/react'
import axe from 'axe-core'
import { renderWithProviders } from '../test/test-utils'
import SLOOverview from './SLOOverview'

vi.mock('@/api/hooks', () => ({
  useDecisionSummary: () => ({ data: undefined, isLoading: false }),
  useDecisions: () => ({ data: undefined, isLoading: false }),
  useServiceObjectives: () => ({
    data: [
      {
        namespace: 'demo',
        name: 'api-slo',
        targetName: 'api-gateway',
        targetKind: 'Deployment',
        compliant: true,
        budgetRemainingPercent: 88,
        objectives: [
          { metric: 'availability', target: '99.9%', burnRate: 0.5 },
          { metric: 'latency_p99', target: '500ms', burnRate: 2 },
        ],
      },
      {
        namespace: 'demo',
        name: 'pay-slo',
        targetName: 'payment-service',
        targetKind: 'Deployment',
        objectives: [
          { metric: 'availability', target: '99%', burnRate: 0.2 },
          { metric: 'latency_p99', target: '1s', burnRate: 0.8 },
        ],
      },
    ],
    isLoading: false,
    isError: false,
  }),
}))

describe('SLOOverview', () => {
  it('renders the page heading', () => {
    renderWithProviders(<SLOOverview />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('SLO Overview')
  })

  it('renders all four stat card labels', () => {
    renderWithProviders(<SLOOverview />)
    expect(screen.getByText(/decisions \/ hr/i)).toBeInTheDocument()
    expect(screen.getByText(/avg confidence/i)).toBeInTheDocument()
    expect(screen.getByText(/total \(24h\)/i)).toBeInTheDocument()
    expect(screen.getByText(/top trigger/i)).toBeInTheDocument()
  })

  it('shows dash placeholders in stat cards when data is loading', () => {
    renderWithProviders(<SLOOverview />)
    // useDecisionSummary returns undefined data — stat card values show "—"
    const dashes = screen.getAllByText('—')
    expect(dashes.length).toBeGreaterThanOrEqual(3)
  })

  it('renders compliance heatmap table with services and metrics', () => {
    renderWithProviders(<SLOOverview />)
    expect(screen.getAllByText('api-gateway').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('payment-service').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByRole('columnheader', { name: 'availability' })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'latency_p99' })).toBeInTheDocument()
  })

  it('renders Recent Decisions section with empty state when journal is empty', () => {
    renderWithProviders(<SLOOverview />)
    expect(screen.getByText(/recent decisions/i)).toBeInTheDocument()
    expect(screen.getByText(/no decisions recorded yet/i)).toBeInTheDocument()
  })

  it('has no serious or critical accessibility violations', async () => {
    const { container } = renderWithProviders(<SLOOverview />)
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
