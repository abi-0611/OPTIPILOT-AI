import { describe, it, expect, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import { renderWithProviders } from '../test/test-utils'
import DecisionExplorer from './DecisionExplorer'

vi.mock('@/api/hooks', () => ({
  useDecisions: () => ({ data: undefined, isLoading: false }),
  useDecisionExplain: () => ({ data: undefined, isLoading: false }),
}))

describe('DecisionExplorer', () => {
  it('renders the page heading', () => {
    renderWithProviders(<DecisionExplorer />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Decision Explorer')
  })

  it('shows the search input and trigger filter', () => {
    renderWithProviders(<DecisionExplorer />)
    expect(
      screen.getByRole('textbox', { name: /search decisions/i }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('combobox', { name: /filter by trigger/i }),
    ).toBeInTheDocument()
  })

  it('renders all 5 mock decisions and shows count', () => {
    renderWithProviders(<DecisionExplorer />)
    expect(screen.getByText('5 decisions')).toBeInTheDocument()
    expect(screen.getByText('api-gateway')).toBeInTheDocument()
    expect(screen.getByText('payment-service')).toBeInTheDocument()
    expect(screen.getByText('worker')).toBeInTheDocument()
  })

  it('filters decisions by typing a service name', async () => {
    const user = userEvent.setup()
    renderWithProviders(<DecisionExplorer />)
    const input = screen.getByRole('textbox', { name: /search decisions/i })
    await user.type(input, 'worker')
    expect(screen.getByText('1 decision')).toBeInTheDocument()
    expect(screen.getByText('worker')).toBeInTheDocument()
    expect(screen.queryByText('api-gateway')).not.toBeInTheDocument()
  })

  it('expands a decision row and shows narrative section', async () => {
    const user = userEvent.setup()
    renderWithProviders(<DecisionExplorer />)
    const rows = screen.getAllByRole('button')
    // Click the first decision card to expand it
    await user.click(rows[0])
    // The Label component renders exactly "Narrative" — distinct from subtitle text
    expect(screen.getByText('Narrative')).toBeInTheDocument()
  })

  it('has no serious or critical accessibility violations', async () => {
    const { container } = renderWithProviders(<DecisionExplorer />)
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
