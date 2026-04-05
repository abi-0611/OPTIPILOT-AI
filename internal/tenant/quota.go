package tenant

import "fmt"

// ResourceDelta describes the change a scaling action would cause for a tenant.
type ResourceDelta struct {
	AdditionalCores     float64
	AdditionalMemoryGiB float64
	AdditionalCostUSD   float64 // monthly cost delta
}

// QuotaResult holds the outcome of a quota check.
type QuotaResult struct {
	Allowed bool
	Reason  string
}

// CheckQuota verifies whether applying delta to the tenant's current usage
// would exceed any hard budget limit. Returns allowed=true if within limits,
// or allowed=false with a human-readable reason if any limit would be breached.
//
// Limits of 0 are treated as unlimited (no cap).
func CheckQuota(state *TenantState, delta ResourceDelta) QuotaResult {
	if state == nil {
		return QuotaResult{Allowed: false, Reason: "tenant state is nil"}
	}

	// Check CPU cores.
	if state.MaxCores > 0 {
		projected := state.CurrentCores + delta.AdditionalCores
		limit := float64(state.MaxCores)
		if projected > limit {
			return QuotaResult{
				Allowed: false,
				Reason: fmt.Sprintf("cores quota exceeded: projected %.1f > limit %.0f (current %.1f + delta %.1f)",
					projected, limit, state.CurrentCores, delta.AdditionalCores),
			}
		}
	}

	// Check memory.
	if state.MaxMemoryGiB > 0 {
		projected := state.CurrentMemoryGiB + delta.AdditionalMemoryGiB
		limit := float64(state.MaxMemoryGiB)
		if projected > limit {
			return QuotaResult{
				Allowed: false,
				Reason: fmt.Sprintf("memory quota exceeded: projected %.1f GiB > limit %.0f GiB (current %.1f + delta %.1f)",
					projected, limit, state.CurrentMemoryGiB, delta.AdditionalMemoryGiB),
			}
		}
	}

	// Check monthly cost.
	if state.MaxMonthlyCostUSD > 0 {
		projected := state.CurrentCostUSD + delta.AdditionalCostUSD
		if projected > state.MaxMonthlyCostUSD {
			return QuotaResult{
				Allowed: false,
				Reason: fmt.Sprintf("cost quota exceeded: projected $%.2f > limit $%.2f (current $%.2f + delta $%.2f)",
					projected, state.MaxMonthlyCostUSD, state.CurrentCostUSD, delta.AdditionalCostUSD),
			}
		}
	}

	return QuotaResult{Allowed: true}
}
