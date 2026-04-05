package tenant

// ResourceShare is the output of the fair-share algorithm for one tenant.
type ResourceShare struct {
	Name            string
	GuaranteedCores float64 // phase 1: guaranteed % of cluster
	BurstCores      float64 // phase 2: extra capacity by weight
	MaxCores        float64 // phase 3: capped at maxBurstPercent
	TotalCores      float64 // guaranteed + burst, capped
}

// FairShareInput describes one tenant for the allocation algorithm.
type FairShareInput struct {
	Name                   string
	Weight                 int32
	GuaranteedCoresPercent int32 // % of cluster guaranteed
	Burstable              bool
	MaxBurstPercent        int32   // max % of cluster (cap); 0 = no cap
	CurrentCores           float64 // for allocation status
}

// ComputeFairShares runs the three-phase fair-share allocation.
//
//  1. Guarantee phase — allocate guaranteedCoresPercent of cluster to each tenant.
//  2. Burst phase — distribute remaining capacity proportionally by weight to burstable tenants.
//  3. Cap phase — enforce maxBurstPercent ceiling; reclaim excess and redistribute.
//
// clusterCores is the total available CPU cores in the cluster.
func ComputeFairShares(clusterCores float64, inputs []FairShareInput) []ResourceShare {
	if len(inputs) == 0 || clusterCores <= 0 {
		return nil
	}

	shares := make([]ResourceShare, len(inputs))

	// ── Phase 1: Guarantee ──────────────────────────────────────────────────
	totalGuaranteed := 0.0
	for i, in := range inputs {
		g := clusterCores * float64(in.GuaranteedCoresPercent) / 100.0
		shares[i] = ResourceShare{
			Name:            in.Name,
			GuaranteedCores: g,
		}
		totalGuaranteed += g
	}

	remaining := clusterCores - totalGuaranteed
	if remaining < 0 {
		remaining = 0
	}

	// ── Phase 2: Burst (weight-proportional) ────────────────────────────────
	// Only burstable tenants participate.
	totalWeight := int32(0)
	for _, in := range inputs {
		if in.Burstable {
			totalWeight += in.Weight
		}
	}

	if totalWeight > 0 && remaining > 0 {
		for i, in := range inputs {
			if in.Burstable {
				burst := remaining * float64(in.Weight) / float64(totalWeight)
				shares[i].BurstCores = burst
			}
		}
	}

	// ── Phase 3: Cap + reclaim ──────────────────────────────────────────────
	// Enforce maxBurstPercent ceiling. Reclaimed capacity is redistributed
	// among uncapped burstable tenants in proportion to weight. We iterate
	// until no more reclamation is needed (converges quickly).
	for round := 0; round < 10; round++ {
		reclaimed := 0.0
		uncappedWeight := int32(0)
		anyReclaimed := false

		for i, in := range inputs {
			total := shares[i].GuaranteedCores + shares[i].BurstCores
			if in.MaxBurstPercent > 0 {
				cap := clusterCores * float64(in.MaxBurstPercent) / 100.0
				if total > cap {
					excess := total - cap
					shares[i].BurstCores -= excess
					if shares[i].BurstCores < 0 {
						shares[i].BurstCores = 0
					}
					reclaimed += excess
					anyReclaimed = true
				}
			}
		}

		if !anyReclaimed || reclaimed < 0.001 {
			break
		}

		// Redistribute reclaimed among uncapped burstable tenants.
		for i, in := range inputs {
			if !in.Burstable {
				continue
			}
			total := shares[i].GuaranteedCores + shares[i].BurstCores
			if in.MaxBurstPercent > 0 {
				cap := clusterCores * float64(in.MaxBurstPercent) / 100.0
				if total >= cap-0.001 {
					continue // already at cap
				}
			}
			uncappedWeight += in.Weight
		}

		if uncappedWeight > 0 {
			for i, in := range inputs {
				if !in.Burstable {
					continue
				}
				total := shares[i].GuaranteedCores + shares[i].BurstCores
				if in.MaxBurstPercent > 0 {
					cap := clusterCores * float64(in.MaxBurstPercent) / 100.0
					if total >= cap-0.001 {
						continue
					}
				}
				extra := reclaimed * float64(in.Weight) / float64(uncappedWeight)
				shares[i].BurstCores += extra
			}
		}
	}

	// Final: apply caps one last time and compute MaxCores + TotalCores.
	for i, in := range inputs {
		total := shares[i].GuaranteedCores + shares[i].BurstCores
		if in.MaxBurstPercent > 0 {
			cap := clusterCores * float64(in.MaxBurstPercent) / 100.0
			if total > cap {
				shares[i].BurstCores = cap - shares[i].GuaranteedCores
				if shares[i].BurstCores < 0 {
					shares[i].BurstCores = 0
				}
				total = shares[i].GuaranteedCores + shares[i].BurstCores
			}
			shares[i].MaxCores = cap
		} else {
			shares[i].MaxCores = clusterCores // no cap means cluster-wide max
		}
		shares[i].TotalCores = total
	}

	return shares
}

// AllocationStatusFor determines the allocation status given usage and share.
func AllocationStatusFor(currentCores float64, share ResourceShare) string {
	if share.GuaranteedCores <= 0 {
		if currentCores > 0 {
			return "bursting"
		}
		return "under_allocated"
	}

	ratio := currentCores / share.GuaranteedCores

	switch {
	case currentCores > share.TotalCores:
		return "throttled"
	case ratio > 1.0:
		return "bursting"
	case ratio >= 0.8:
		return "guaranteed"
	default:
		return "under_allocated"
	}
}
