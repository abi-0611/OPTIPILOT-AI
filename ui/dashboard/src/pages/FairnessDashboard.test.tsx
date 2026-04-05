import { describe, it, expect, vi } from 'vitest'
import { screen } from '@testing-library/react'
import axe from 'axe-core'
import { renderWithProviders } from '../test/test-utils'
import FairnessDashboard from './FairnessDashboard'

vi.mock('@/api/hooks', () => ({
  useTenants: () => ({ data: undefined, isLoading: false }),
  useFairness: () => ({ data: undefined, isLoading: false }),
}))

describe('FairnessDashboard', () => {
  it('renders the page heading', () => {
    renderWithProviders(<FairnessDashboard />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Fairness Dashboard')
  })

  it('renders the three summary stat cards', () => {
    renderWithProviders(<FairnessDashboard />)
    expect(screen.getByText(/global fairness index/i)).toBeInTheDocument()
    expect(screen.getByText(/active tenants/i)).toBeInTheDocument()
    expect(screen.getByText(/noisy alerts/i)).toBeInTheDocument()
  })

  it('shows noisy-neighbor warning banner for team-beta', () => {
    renderWithProviders(<FairnessDashboard />)
    // MOCK_TENANTS has team-beta with is_noisy: true
    expect(screen.getByText(/noisy neighbor detected.*team-beta/i)).toBeInTheDocument()
  })

  it('shows victim tenant error banner for team-gamma', () => {
    renderWithProviders(<FairnessDashboard />)
    // MOCK_TENANTS has team-gamma with is_victim: true
    expect(screen.getByText(/victim tenant.*team-gamma/i)).toBeInTheDocument()
  })

  it('renders all mock tenants in allocation and scores sections', () => {
    renderWithProviders(<FairnessDashboard />)
    for (const name of ['team-alpha', 'team-beta', 'team-gamma', 'team-delta']) {
      expect(screen.getAllByText(name).length).toBeGreaterThanOrEqual(1)
    }
  })

  it('has no serious or critical accessibility violations', async () => {
    const { container } = renderWithProviders(<FairnessDashboard />)
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
