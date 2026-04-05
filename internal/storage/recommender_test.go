package storage

import (
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// ClassifyProfile
// ---------------------------------------------------------------------------

func TestClassifyProfile_Idle(t *testing.T) {
	m := PVCMetrics{ReadIOPS: 0, WriteIOPS: 0, ReadThroughputMBs: 0}
	if p := ClassifyProfile(m); p != ProfileIdle {
		t.Errorf("got %s, want idle", p)
	}
}

func TestClassifyProfile_ReadHeavy(t *testing.T) {
	m := PVCMetrics{ReadIOPS: 800, WriteIOPS: 100, ReadThroughputMBs: 5, WriteThroughputMBs: 1}
	if p := ClassifyProfile(m); p != ProfileReadHeavy {
		t.Errorf("got %s, want read-heavy", p)
	}
}

func TestClassifyProfile_WriteHeavy(t *testing.T) {
	m := PVCMetrics{ReadIOPS: 50, WriteIOPS: 900, ReadThroughputMBs: 1, WriteThroughputMBs: 10}
	if p := ClassifyProfile(m); p != ProfileWriteHeavy {
		t.Errorf("got %s, want write-heavy", p)
	}
}

func TestClassifyProfile_Mixed(t *testing.T) {
	m := PVCMetrics{ReadIOPS: 400, WriteIOPS: 400, ReadThroughputMBs: 15, WriteThroughputMBs: 15}
	if p := ClassifyProfile(m); p != ProfileMixed {
		t.Errorf("got %s, want mixed", p)
	}
}

func TestClassifyProfile_Sequential(t *testing.T) {
	// High throughput, low IOPS → large I/O sizes.
	m := PVCMetrics{ReadIOPS: 10, WriteIOPS: 10, ReadThroughputMBs: 200, WriteThroughputMBs: 200}
	if p := ClassifyProfile(m); p != ProfileSequential {
		t.Errorf("got %s, want sequential", p)
	}
}

func TestClassifyProfile_Random(t *testing.T) {
	// High IOPS, tiny I/O sizes.
	m := PVCMetrics{ReadIOPS: 5000, WriteIOPS: 5000, ReadThroughputMBs: 10, WriteThroughputMBs: 10}
	if p := ClassifyProfile(m); p != ProfileRandom {
		t.Errorf("got %s, want random", p)
	}
}

func TestClassifyProfile_Bursty(t *testing.T) {
	m := PVCMetrics{ReadIOPS: 100, WriteIOPS: 100, QueueDepth: 20, ReadThroughputMBs: 1, WriteThroughputMBs: 1}
	if p := ClassifyProfile(m); p != ProfileBursty {
		t.Errorf("got %s, want bursty", p)
	}
}

// ---------------------------------------------------------------------------
// PVCMetrics helpers
// ---------------------------------------------------------------------------

func TestTotalIOPS(t *testing.T) {
	m := PVCMetrics{ReadIOPS: 300, WriteIOPS: 700}
	if m.TotalIOPS() != 1000 {
		t.Errorf("got %f", m.TotalIOPS())
	}
}

func TestTotalThroughput(t *testing.T) {
	m := PVCMetrics{ReadThroughputMBs: 50, WriteThroughputMBs: 150}
	if m.TotalThroughput() != 200 {
		t.Errorf("got %f", m.TotalThroughput())
	}
}

func TestAvgLatency_Both(t *testing.T) {
	m := PVCMetrics{ReadLatencyMs: 2, WriteLatencyMs: 4}
	if m.AvgLatency() != 3 {
		t.Errorf("got %f", m.AvgLatency())
	}
}

func TestAvgLatency_ReadOnly(t *testing.T) {
	m := PVCMetrics{ReadLatencyMs: 5}
	if m.AvgLatency() != 5 {
		t.Errorf("got %f", m.AvgLatency())
	}
}

func TestAvgLatency_None(t *testing.T) {
	m := PVCMetrics{}
	if m.AvgLatency() != 0 {
		t.Errorf("got %f", m.AvgLatency())
	}
}

// ---------------------------------------------------------------------------
// DefaultCatalog
// ---------------------------------------------------------------------------

func TestDefaultCatalog_HasEntries(t *testing.T) {
	c := DefaultCatalog()
	if len(c) != 3 {
		t.Errorf("got %d entries", len(c))
	}
	names := map[string]bool{}
	for _, e := range c {
		names[e.Name] = true
	}
	for _, want := range []string{"gp3", "io2", "st1"} {
		if !names[want] {
			t.Errorf("missing %s", want)
		}
	}
}

// ---------------------------------------------------------------------------
// Recommend — profile-driven class selection
// ---------------------------------------------------------------------------

func TestRecommend_MixedPreferGP3(t *testing.T) {
	r := NewRecommender(nil)
	m := PVCMetrics{
		Namespace: "ns", PVCName: "data", CapacityGiB: 100,
		ReadIOPS: 400, WriteIOPS: 400,
		ReadThroughputMBs: 15, WriteThroughputMBs: 15,
		CurrentStorageClass: "gp2",
	}
	rec := r.Recommend(m)
	if rec.RecommendedClass != "gp3" {
		t.Errorf("got %s, want gp3", rec.RecommendedClass)
	}
	if rec.Profile != ProfileMixed {
		t.Errorf("profile=%s", rec.Profile)
	}
}

func TestRecommend_HighIOPSPreferIO2(t *testing.T) {
	r := NewRecommender(nil)
	m := PVCMetrics{
		Namespace: "ns", PVCName: "db",
		ReadIOPS: 5000, WriteIOPS: 5000,
		ReadThroughputMBs: 10, WriteThroughputMBs: 10,
		CapacityGiB: 50,
	}
	rec := r.Recommend(m)
	if rec.RecommendedClass != "io2" {
		t.Errorf("got %s, want io2", rec.RecommendedClass)
	}
}

func TestRecommend_SequentialPreferST1(t *testing.T) {
	r := NewRecommender(nil)
	m := PVCMetrics{
		ReadIOPS: 10, WriteIOPS: 10,
		ReadThroughputMBs: 200, WriteThroughputMBs: 200,
		CapacityGiB: 500,
	}
	rec := r.Recommend(m)
	if rec.RecommendedClass != "st1" {
		t.Errorf("got %s, want st1", rec.RecommendedClass)
	}
}

func TestRecommend_IdlePreferGP3(t *testing.T) {
	r := NewRecommender(nil)
	m := PVCMetrics{CapacityGiB: 10}
	rec := r.Recommend(m)
	if rec.RecommendedClass != "gp3" {
		t.Errorf("got %s, want gp3 for idle", rec.RecommendedClass)
	}
}

func TestRecommend_BurstyPreferIO2(t *testing.T) {
	r := NewRecommender(nil)
	m := PVCMetrics{
		ReadIOPS: 100, WriteIOPS: 100, QueueDepth: 20,
		ReadThroughputMBs: 1, WriteThroughputMBs: 1,
		CapacityGiB: 100,
	}
	rec := r.Recommend(m)
	if rec.RecommendedClass != "io2" {
		t.Errorf("got %s, want io2 for bursty", rec.RecommendedClass)
	}
}

// ---------------------------------------------------------------------------
// Cost estimation
// ---------------------------------------------------------------------------

func TestRecommend_CostEstimation(t *testing.T) {
	r := NewRecommender(nil)
	m := PVCMetrics{
		Namespace: "ns", PVCName: "vol",
		ReadIOPS: 400, WriteIOPS: 400,
		ReadThroughputMBs: 15, WriteThroughputMBs: 15,
		CurrentStorageClass: "io2", CapacityGiB: 100,
	}
	rec := r.Recommend(m)
	// current=io2 ($0.125*100=12.5), recommended=gp3 ($0.08*100=8.0)
	if Round2(rec.EstMonthlyCostCur) != 12.5 {
		t.Errorf("cur cost=%f, want 12.50", rec.EstMonthlyCostCur)
	}
	if Round2(rec.EstMonthlyCostNew) != 8.0 {
		t.Errorf("new cost=%f, want 8.00", rec.EstMonthlyCostNew)
	}
	if Round2(rec.EstMonthlySavings) != 4.5 {
		t.Errorf("savings=%f, want 4.50", rec.EstMonthlySavings)
	}
}

func TestRecommend_UnknownCurrentClass(t *testing.T) {
	r := NewRecommender(nil)
	m := PVCMetrics{
		ReadIOPS: 400, WriteIOPS: 400,
		ReadThroughputMBs: 5, WriteThroughputMBs: 5,
		CurrentStorageClass: "unknown", CapacityGiB: 50,
	}
	rec := r.Recommend(m)
	if rec.EstMonthlyCostCur != 0 {
		t.Errorf("expected 0 for unknown class, got %f", rec.EstMonthlyCostCur)
	}
}

func TestRecommend_ZeroCapacity(t *testing.T) {
	r := NewRecommender(nil)
	m := PVCMetrics{ReadIOPS: 100, WriteIOPS: 100, ReadThroughputMBs: 1, WriteThroughputMBs: 1}
	rec := r.Recommend(m)
	// capacity defaults to 1 GiB
	if rec.EstMonthlyCostNew <= 0 {
		t.Error("expected positive cost even with zero capacity")
	}
}

// ---------------------------------------------------------------------------
// Annotations
// ---------------------------------------------------------------------------

func TestRecommend_Annotations(t *testing.T) {
	r := NewRecommender(nil)
	m := PVCMetrics{
		Namespace: "ns", PVCName: "vol",
		ReadIOPS: 400, WriteIOPS: 400,
		ReadThroughputMBs: 5, WriteThroughputMBs: 5,
		CapacityGiB: 100,
	}
	rec := r.Recommend(m)
	want := []string{
		"optipilot.ai/storage-profile",
		"optipilot.ai/storage-recommended",
		"optipilot.ai/storage-cost-current",
		"optipilot.ai/storage-cost-new",
		"optipilot.ai/storage-savings",
	}
	for _, k := range want {
		if _, ok := rec.Annotations[k]; !ok {
			t.Errorf("missing annotation %s", k)
		}
	}
}

// ---------------------------------------------------------------------------
// Reason
// ---------------------------------------------------------------------------

func TestRecommend_ReasonNotEmpty(t *testing.T) {
	r := NewRecommender(nil)
	rec := r.Recommend(PVCMetrics{ReadIOPS: 100, WriteIOPS: 100, ReadThroughputMBs: 1, WriteThroughputMBs: 1, CapacityGiB: 10})
	if rec.Reason == "" {
		t.Error("expected non-empty reason")
	}
}

// ---------------------------------------------------------------------------
// RecommendAll / ChangesOnly
// ---------------------------------------------------------------------------

func TestRecommendAll(t *testing.T) {
	r := NewRecommender(nil)
	pvcs := []PVCMetrics{
		{Namespace: "a", PVCName: "v1", ReadIOPS: 400, WriteIOPS: 400, ReadThroughputMBs: 5, WriteThroughputMBs: 5, CapacityGiB: 10},
		{Namespace: "b", PVCName: "v2", ReadIOPS: 10, WriteIOPS: 10, ReadThroughputMBs: 200, WriteThroughputMBs: 200, CapacityGiB: 20},
	}
	recs := r.RecommendAll(pvcs)
	if len(recs) != 2 {
		t.Fatalf("got %d", len(recs))
	}
}

func TestChangesOnly(t *testing.T) {
	recs := []Recommendation{
		{CurrentClass: "gp3", RecommendedClass: "gp3"},
		{CurrentClass: "gp2", RecommendedClass: "gp3"},
		{CurrentClass: "io2", RecommendedClass: "st1"},
	}
	changes := ChangesOnly(recs)
	if len(changes) != 2 {
		t.Errorf("got %d, want 2", len(changes))
	}
}

func TestChangesOnly_CaseInsensitive(t *testing.T) {
	recs := []Recommendation{
		{CurrentClass: "GP3", RecommendedClass: "gp3"},
	}
	if len(ChangesOnly(recs)) != 0 {
		t.Error("expected case-insensitive match")
	}
}

// ---------------------------------------------------------------------------
// Custom catalog
// ---------------------------------------------------------------------------

func TestNewRecommender_CustomCatalog(t *testing.T) {
	catalog := []StorageClassProfile{
		{Name: "fast", MaxIOPS: 100000, MaxThroughput: 2000, AvgLatencyMs: 0.1, CostPerGiBMonth: 0.5, BestFor: []WorkloadProfile{ProfileRandom}},
	}
	r := NewRecommender(catalog)
	m := PVCMetrics{ReadIOPS: 5000, WriteIOPS: 5000, ReadThroughputMBs: 10, WriteThroughputMBs: 10, CapacityGiB: 100}
	rec := r.Recommend(m)
	if rec.RecommendedClass != "fast" {
		t.Errorf("got %s, want fast", rec.RecommendedClass)
	}
}

// ---------------------------------------------------------------------------
// profileMatch
// ---------------------------------------------------------------------------

func TestProfileMatch_True(t *testing.T) {
	if !profileMatch([]WorkloadProfile{ProfileMixed, ProfileReadHeavy}, ProfileReadHeavy) {
		t.Error("expected true")
	}
}

func TestProfileMatch_False(t *testing.T) {
	if profileMatch([]WorkloadProfile{ProfileMixed}, ProfileRandom) {
		t.Error("expected false")
	}
}

// ---------------------------------------------------------------------------
// Round2
// ---------------------------------------------------------------------------

func TestRound2(t *testing.T) {
	if Round2(3.1459) != 3.15 {
		t.Errorf("got %f", Round2(3.1459))
	}
	if Round2(0.0) != 0.0 {
		t.Errorf("got %f", Round2(0.0))
	}
}

// ---------------------------------------------------------------------------
// classScore edge cases
// ---------------------------------------------------------------------------

func TestClassScore_IOPSExceeded(t *testing.T) {
	c := StorageClassProfile{Name: "tiny", MaxIOPS: 100, MaxThroughput: 100, AvgLatencyMs: 1, CostPerGiBMonth: 0.01, BestFor: []WorkloadProfile{ProfileMixed}}
	m := PVCMetrics{ReadIOPS: 500, WriteIOPS: 500, ReadThroughputMBs: 5, WriteThroughputMBs: 5}
	score := classScore(m, ProfileMixed, c)
	// Should include IOPS penalty (50 * 1000/100 = 500)
	if score < 500 {
		t.Errorf("expected high penalty, got %f", score)
	}
}

func TestClassScore_ThroughputExceeded(t *testing.T) {
	c := StorageClassProfile{Name: "slow", MaxIOPS: 100000, MaxThroughput: 10, AvgLatencyMs: 1, CostPerGiBMonth: 0.01, BestFor: []WorkloadProfile{ProfileSequential}}
	m := PVCMetrics{ReadIOPS: 10, WriteIOPS: 10, ReadThroughputMBs: 50, WriteThroughputMBs: 50}
	score := classScore(m, ProfileSequential, c)
	if score < 500 {
		t.Errorf("expected throughput penalty, got %f", score)
	}
}

func TestClassScore_NoProfileMatch(t *testing.T) {
	c := StorageClassProfile{Name: "x", MaxIOPS: 100000, MaxThroughput: 1000, AvgLatencyMs: 1, CostPerGiBMonth: 0.01, BestFor: []WorkloadProfile{ProfileIdle}}
	m := PVCMetrics{ReadIOPS: 1000, WriteIOPS: 1000, ReadThroughputMBs: 5, WriteThroughputMBs: 5}
	score := classScore(m, ProfileMixed, c)
	if score < 100 {
		t.Errorf("expected profile mismatch penalty, got %f", score)
	}
}

// ---------------------------------------------------------------------------
// findClass
// ---------------------------------------------------------------------------

func TestFindClass_Found(t *testing.T) {
	r := NewRecommender(nil)
	if c := r.findClass("gp3"); c == nil || c.Name != "gp3" {
		t.Error("expected gp3")
	}
}

func TestFindClass_NotFound(t *testing.T) {
	r := NewRecommender(nil)
	if c := r.findClass("nonexistent"); c != nil {
		t.Error("expected nil")
	}
}

func TestFindClass_CaseInsensitive(t *testing.T) {
	r := NewRecommender(nil)
	if c := r.findClass("GP3"); c == nil {
		t.Error("expected case-insensitive match")
	}
}

// ---------------------------------------------------------------------------
// Negative savings (upgrade costs more)
// ---------------------------------------------------------------------------

func TestRecommend_NegativeSavings(t *testing.T) {
	r := NewRecommender(nil)
	m := PVCMetrics{
		ReadIOPS: 5000, WriteIOPS: 5000,
		ReadThroughputMBs: 10, WriteThroughputMBs: 10,
		CurrentStorageClass: "st1", CapacityGiB: 100,
	}
	rec := r.Recommend(m)
	if rec.RecommendedClass != "io2" {
		t.Fatalf("expected io2, got %s", rec.RecommendedClass)
	}
	// st1 cheaper than io2 → negative savings
	if rec.EstMonthlySavings >= 0 {
		t.Errorf("expected negative savings, got %f", rec.EstMonthlySavings)
	}
}

// ---------------------------------------------------------------------------
// Verify NaN never appears
// ---------------------------------------------------------------------------

func TestRecommend_NoNaN(t *testing.T) {
	r := NewRecommender(nil)
	for _, m := range []PVCMetrics{
		{},            // all zeros
		{ReadIOPS: 1}, // minimal
		{WriteIOPS: 99999, WriteThroughputMBs: 99999, CapacityGiB: 0.01},
	} {
		rec := r.Recommend(m)
		if math.IsNaN(rec.EstMonthlyCostNew) || math.IsNaN(rec.EstMonthlySavings) {
			t.Errorf("NaN in recommendation for %+v", m)
		}
	}
}
