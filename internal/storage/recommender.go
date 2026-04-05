package storage

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// I/O Metrics
// ---------------------------------------------------------------------------

// PVCMetrics holds observed storage I/O metrics for a single PVC.
type PVCMetrics struct {
	Namespace string
	PVCName   string

	// IOPS
	ReadIOPS  float64
	WriteIOPS float64

	// Throughput in MB/s
	ReadThroughputMBs  float64
	WriteThroughputMBs float64

	// Latency in milliseconds
	ReadLatencyMs  float64
	WriteLatencyMs float64

	// Queue depth (average outstanding I/O)
	QueueDepth float64

	// Current storage class (empty if unknown)
	CurrentStorageClass string

	// Capacity in GiB (for cost estimation)
	CapacityGiB float64
}

// TotalIOPS returns combined read + write IOPS.
func (m PVCMetrics) TotalIOPS() float64 { return m.ReadIOPS + m.WriteIOPS }

// TotalThroughput returns combined read + write throughput in MB/s.
func (m PVCMetrics) TotalThroughput() float64 { return m.ReadThroughputMBs + m.WriteThroughputMBs }

// AvgLatency returns the average of read and write latency.
func (m PVCMetrics) AvgLatency() float64 {
	if m.ReadLatencyMs == 0 && m.WriteLatencyMs == 0 {
		return 0
	}
	count := 0.0
	total := 0.0
	if m.ReadLatencyMs > 0 {
		total += m.ReadLatencyMs
		count++
	}
	if m.WriteLatencyMs > 0 {
		total += m.WriteLatencyMs
		count++
	}
	return total / count
}

// ---------------------------------------------------------------------------
// Workload Profile
// ---------------------------------------------------------------------------

// WorkloadProfile describes an I/O workload pattern.
type WorkloadProfile string

const (
	ProfileReadHeavy  WorkloadProfile = "read-heavy"
	ProfileWriteHeavy WorkloadProfile = "write-heavy"
	ProfileMixed      WorkloadProfile = "mixed"
	ProfileSequential WorkloadProfile = "sequential"
	ProfileRandom     WorkloadProfile = "random"
	ProfileBursty     WorkloadProfile = "bursty"
	ProfileIdle       WorkloadProfile = "idle"
)

// ClassifyProfile determines the workload profile from metrics.
func ClassifyProfile(m PVCMetrics) WorkloadProfile {
	totalIOPS := m.TotalIOPS()
	if totalIOPS < 1 && m.TotalThroughput() < 0.1 {
		return ProfileIdle
	}

	// Bursty: high queue depth relative to IOPS.
	if totalIOPS > 0 && m.QueueDepth/totalIOPS > 0.05 {
		return ProfileBursty
	}

	// Sequential: high throughput relative to IOPS (large I/O sizes).
	if totalIOPS > 0 {
		avgIOSizeKB := (m.TotalThroughput() * 1024) / totalIOPS
		if avgIOSizeKB > 128 {
			return ProfileSequential
		}
	}

	// Read/Write heavy classification (before random check).
	readRatio := 0.0
	if totalIOPS > 0 {
		readRatio = m.ReadIOPS / totalIOPS
	}
	if readRatio > 0.7 {
		return ProfileReadHeavy
	}
	if readRatio < 0.3 {
		return ProfileWriteHeavy
	}

	// Random: high IOPS with small I/O sizes.
	if totalIOPS > 500 {
		avgIOSizeKB := 0.0
		if totalIOPS > 0 {
			avgIOSizeKB = (m.TotalThroughput() * 1024) / totalIOPS
		}
		if avgIOSizeKB < 32 {
			return ProfileRandom
		}
	}

	return ProfileMixed
}

// ---------------------------------------------------------------------------
// Storage Class Catalog
// ---------------------------------------------------------------------------

// StorageClassProfile describes a storage class and its characteristics.
type StorageClassProfile struct {
	Name            string
	MaxIOPS         float64
	MaxThroughput   float64 // MB/s
	AvgLatencyMs    float64
	CostPerGiBMonth float64 // USD/GiB/month
	BestFor         []WorkloadProfile
}

// DefaultCatalog returns a built-in catalog modelled on AWS EBS types.
func DefaultCatalog() []StorageClassProfile {
	return []StorageClassProfile{
		{
			Name: "gp3", MaxIOPS: 16000, MaxThroughput: 1000,
			AvgLatencyMs: 1.5, CostPerGiBMonth: 0.08,
			BestFor: []WorkloadProfile{ProfileMixed, ProfileReadHeavy, ProfileIdle},
		},
		{
			Name: "io2", MaxIOPS: 64000, MaxThroughput: 1000,
			AvgLatencyMs: 0.5, CostPerGiBMonth: 0.125,
			BestFor: []WorkloadProfile{ProfileRandom, ProfileBursty},
		},
		{
			Name: "st1", MaxIOPS: 500, MaxThroughput: 500,
			AvgLatencyMs: 5.0, CostPerGiBMonth: 0.045,
			BestFor: []WorkloadProfile{ProfileSequential, ProfileWriteHeavy},
		},
	}
}

// ---------------------------------------------------------------------------
// Recommendation
// ---------------------------------------------------------------------------

// Recommendation is the output of the storage recommender.
type Recommendation struct {
	PVCNamespace      string
	PVCName           string
	CurrentClass      string
	RecommendedClass  string
	Profile           WorkloadProfile
	Reason            string
	EstMonthlyCostCur float64
	EstMonthlyCostNew float64
	EstMonthlySavings float64
	Annotations       map[string]string
}

// Recommender analyses PVC metrics and recommends optimal storage classes.
type Recommender struct {
	catalog []StorageClassProfile
}

// NewRecommender creates a Recommender with the given catalog.
// If catalog is nil, DefaultCatalog is used.
func NewRecommender(catalog []StorageClassProfile) *Recommender {
	if len(catalog) == 0 {
		catalog = DefaultCatalog()
	}
	return &Recommender{catalog: catalog}
}

// Recommend analyses a single PVC's metrics and returns a recommendation.
func (r *Recommender) Recommend(m PVCMetrics) Recommendation {
	profile := ClassifyProfile(m)

	scored := r.scoreClasses(m, profile)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score < scored[j].score // lower is better
	})

	best := scored[0]
	rec := Recommendation{
		PVCNamespace:     m.Namespace,
		PVCName:          m.PVCName,
		CurrentClass:     m.CurrentStorageClass,
		RecommendedClass: best.class.Name,
		Profile:          profile,
	}

	cap := m.CapacityGiB
	if cap <= 0 {
		cap = 1 // avoid zero
	}

	// Cost estimation.
	if curClass := r.findClass(m.CurrentStorageClass); curClass != nil {
		rec.EstMonthlyCostCur = curClass.CostPerGiBMonth * cap
	}
	rec.EstMonthlyCostNew = best.class.CostPerGiBMonth * cap
	rec.EstMonthlySavings = rec.EstMonthlyCostCur - rec.EstMonthlyCostNew

	rec.Reason = buildReason(m, profile, best.class)
	rec.Annotations = buildAnnotations(rec)

	return rec
}

// RecommendAll processes multiple PVCs and returns recommendations.
// PVCs where the recommended class matches the current class are still included
// (callers may filter).
func (r *Recommender) RecommendAll(pvcs []PVCMetrics) []Recommendation {
	recs := make([]Recommendation, len(pvcs))
	for i, m := range pvcs {
		recs[i] = r.Recommend(m)
	}
	return recs
}

// ChangesOnly filters recommendations to those where the class would change.
func ChangesOnly(recs []Recommendation) []Recommendation {
	var out []Recommendation
	for _, r := range recs {
		if !strings.EqualFold(r.CurrentClass, r.RecommendedClass) {
			out = append(out, r)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Internal scoring
// ---------------------------------------------------------------------------

type scoredClass struct {
	class StorageClassProfile
	score float64
}

func (r *Recommender) scoreClasses(m PVCMetrics, profile WorkloadProfile) []scoredClass {
	scored := make([]scoredClass, len(r.catalog))
	for i, c := range r.catalog {
		scored[i] = scoredClass{class: c, score: classScore(m, profile, c)}
	}
	return scored
}

// classScore computes a penalty score (lower is better).
func classScore(m PVCMetrics, profile WorkloadProfile, c StorageClassProfile) float64 {
	score := 0.0

	// 1. Profile affinity (primary signal).
	if !profileMatch(c.BestFor, profile) {
		score += 100
	}

	// 2. Capacity penalty: cannot serve required IOPS.
	if c.MaxIOPS > 0 && m.TotalIOPS() > c.MaxIOPS {
		score += 50 * (m.TotalIOPS() / c.MaxIOPS)
	}

	// 3. Throughput penalty.
	if c.MaxThroughput > 0 && m.TotalThroughput() > c.MaxThroughput {
		score += 50 * (m.TotalThroughput() / c.MaxThroughput)
	}

	// 4. Latency penalty (prefer lower latency).
	if c.AvgLatencyMs > 0 {
		score += c.AvgLatencyMs
	}

	// 5. Cost (minor tiebreaker).
	score += c.CostPerGiBMonth * 10

	return score
}

func profileMatch(supported []WorkloadProfile, target WorkloadProfile) bool {
	for _, p := range supported {
		if p == target {
			return true
		}
	}
	return false
}

func (r *Recommender) findClass(name string) *StorageClassProfile {
	for i := range r.catalog {
		if strings.EqualFold(r.catalog[i].Name, name) {
			return &r.catalog[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Reason / Annotation builders
// ---------------------------------------------------------------------------

func buildReason(m PVCMetrics, profile WorkloadProfile, best StorageClassProfile) string {
	return fmt.Sprintf(
		"workload=%s iops=%.0f throughput=%.1fMB/s latency=%.1fms → %s (max-iops=%g, cost=$%.3f/GiB/mo)",
		profile, m.TotalIOPS(), m.TotalThroughput(), m.AvgLatency(), best.Name, best.MaxIOPS, best.CostPerGiBMonth,
	)
}

func buildAnnotations(rec Recommendation) map[string]string {
	return map[string]string{
		"optipilot.ai/storage-profile":      string(rec.Profile),
		"optipilot.ai/storage-recommended":  rec.RecommendedClass,
		"optipilot.ai/storage-cost-current": fmt.Sprintf("%.2f", rec.EstMonthlyCostCur),
		"optipilot.ai/storage-cost-new":     fmt.Sprintf("%.2f", rec.EstMonthlyCostNew),
		"optipilot.ai/storage-savings":      fmt.Sprintf("%.2f", rec.EstMonthlySavings),
	}
}

// ---------------------------------------------------------------------------
// Utility: round to N decimal places (for display / tests)
// ---------------------------------------------------------------------------

func Round2(v float64) float64 {
	return math.Round(v*100) / 100
}
