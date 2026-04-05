"""Tests for ml/app/accuracy.py — forecast accuracy tracker."""
from __future__ import annotations

import pytest

from app.accuracy import AccuracyTracker


# ── helpers ────────────────────────────────────────────────────────────────────


class FakeClock:
    """Deterministic clock for tests."""

    def __init__(self, start: float = 1_000_000.0) -> None:
        self._now = start

    def time(self) -> float:
        return self._now

    def advance(self, seconds: float) -> None:
        self._now += seconds


@pytest.fixture()
def clock() -> FakeClock:
    return FakeClock()


@pytest.fixture()
def tracker(clock: FakeClock) -> AccuracyTracker:
    return AccuracyTracker(clock=clock)


# ── basic metrics ──────────────────────────────────────────────────────────────


class TestBasicMetrics:
    def test_no_data(self, tracker: AccuracyTracker) -> None:
        assert tracker.mae("svc-a") == 0.0
        assert tracker.mape("svc-a") == 0.0
        assert not tracker.is_fallback_active("svc-a")

    def test_single_record_perfect(self, tracker: AccuracyTracker) -> None:
        tracker.record("svc-a", predicted=100.0, actual=100.0)
        assert tracker.mae("svc-a") == 0.0
        assert tracker.mape("svc-a") == 0.0

    def test_single_record_error(self, tracker: AccuracyTracker) -> None:
        tracker.record("svc-a", predicted=110.0, actual=100.0)
        assert tracker.mae("svc-a") == pytest.approx(10.0)
        assert tracker.mape("svc-a") == pytest.approx(10.0)  # 10/100 * 100

    def test_multiple_records_average(
        self, tracker: AccuracyTracker, clock: FakeClock
    ) -> None:
        tracker.record("svc-a", predicted=110.0, actual=100.0)  # AE=10, APE=10%
        clock.advance(60)
        tracker.record("svc-a", predicted=90.0, actual=100.0)  # AE=10, APE=10%
        assert tracker.mae("svc-a") == pytest.approx(10.0)
        assert tracker.mape("svc-a") == pytest.approx(10.0)

    def test_mae_asymmetric(self, tracker: AccuracyTracker, clock: FakeClock) -> None:
        tracker.record("svc-a", predicted=120.0, actual=100.0)  # AE=20
        clock.advance(60)
        tracker.record("svc-a", predicted=105.0, actual=100.0)  # AE=5
        assert tracker.mae("svc-a") == pytest.approx(12.5)  # (20+5)/2

    def test_mape_zero_actual_ignored(self, tracker: AccuracyTracker) -> None:
        # actual=0 → APE undefined; should not crash, should be excluded.
        tracker.record("svc-a", predicted=10.0, actual=0.0)
        assert tracker.mape("svc-a") == 0.0  # no valid APE points
        assert tracker.mae("svc-a") == pytest.approx(10.0)

    def test_mape_mixed_zero_and_nonzero(
        self, tracker: AccuracyTracker, clock: FakeClock
    ) -> None:
        tracker.record("svc-a", predicted=10.0, actual=0.0)  # excluded from MAPE
        clock.advance(60)
        tracker.record("svc-a", predicted=120.0, actual=100.0)  # APE = 20%
        assert tracker.mape("svc-a") == pytest.approx(20.0)
        assert tracker.mae("svc-a") == pytest.approx(15.0)  # (10+20)/2


# ── fallback logic ─────────────────────────────────────────────────────────────


class TestFallbackLogic:
    def test_no_fallback_when_mape_below_threshold(
        self, tracker: AccuracyTracker, clock: FakeClock
    ) -> None:
        # MAPE = 20% < 30% threshold
        for i in range(10):
            tracker.record("svc-a", predicted=120.0, actual=100.0)
            clock.advance(900)  # 15 min intervals
        assert tracker.mape("svc-a") == pytest.approx(20.0)
        assert not tracker.is_fallback_active("svc-a")

    def test_fallback_activates_after_sustained_high_mape(
        self, clock: FakeClock
    ) -> None:
        tracker = AccuracyTracker(
            mape_threshold=30.0, sustain_seconds=3600.0, clock=clock
        )
        # Record high-MAPE predictions over 1 hour
        for i in range(5):
            tracker.record("svc-a", predicted=150.0, actual=100.0)  # MAPE=50%
            clock.advance(900)  # 15 min = 4500s total after 5 records

        # After 5 records at 15-min intervals = 4 * 900 = 3600s since first record
        assert tracker.mape("svc-a") == pytest.approx(50.0)
        assert tracker.is_fallback_active("svc-a")

    def test_fallback_not_triggered_without_sustained_period(
        self, clock: FakeClock
    ) -> None:
        tracker = AccuracyTracker(
            mape_threshold=30.0, sustain_seconds=3600.0, clock=clock
        )
        # Only 2 records — not enough time elapsed
        tracker.record("svc-a", predicted=150.0, actual=100.0)
        clock.advance(300)  # 5 minutes
        tracker.record("svc-a", predicted=150.0, actual=100.0)
        assert tracker.mape("svc-a") == pytest.approx(50.0)
        assert not tracker.is_fallback_active("svc-a")  # only 300s < 3600s

    def test_fallback_recovers_when_mape_drops(
        self, clock: FakeClock
    ) -> None:
        tracker = AccuracyTracker(
            mape_threshold=30.0, sustain_seconds=60.0, clock=clock
        )
        # Trigger fallback with short sustain period
        tracker.record("svc-a", predicted=150.0, actual=100.0)
        clock.advance(120)
        tracker.record("svc-a", predicted=150.0, actual=100.0)
        assert tracker.is_fallback_active("svc-a")

        # Now record accurate predictions to bring MAPE below threshold
        for _ in range(20):
            clock.advance(60)
            tracker.record("svc-a", predicted=100.0, actual=100.0)

        assert tracker.mape("svc-a") < 30.0
        assert not tracker.is_fallback_active("svc-a")

    def test_short_sustain_triggers_quickly(self, clock: FakeClock) -> None:
        tracker = AccuracyTracker(
            mape_threshold=10.0, sustain_seconds=10.0, clock=clock
        )
        tracker.record("svc-a", predicted=150.0, actual=100.0)
        clock.advance(15)
        tracker.record("svc-a", predicted=150.0, actual=100.0)
        assert tracker.is_fallback_active("svc-a")


# ── multi-service isolation ────────────────────────────────────────────────────


class TestMultiService:
    def test_services_isolated(
        self, tracker: AccuracyTracker, clock: FakeClock
    ) -> None:
        tracker.record("svc-a", predicted=110.0, actual=100.0)
        clock.advance(60)
        tracker.record("svc-b", predicted=200.0, actual=100.0)

        assert tracker.mae("svc-a") == pytest.approx(10.0)
        assert tracker.mae("svc-b") == pytest.approx(100.0)
        assert tracker.mape("svc-a") == pytest.approx(10.0)
        assert tracker.mape("svc-b") == pytest.approx(100.0)

    def test_services_list(self, tracker: AccuracyTracker, clock: FakeClock) -> None:
        tracker.record("svc-a", predicted=100.0, actual=100.0)
        clock.advance(1)
        tracker.record("svc-b", predicted=100.0, actual=100.0)
        services = tracker.services()
        assert "svc-a" in services
        assert "svc-b" in services

    def test_fallback_per_service(self, clock: FakeClock) -> None:
        tracker = AccuracyTracker(
            mape_threshold=30.0, sustain_seconds=10.0, clock=clock
        )
        # svc-a gets bad predictions
        tracker.record("svc-a", predicted=200.0, actual=100.0)
        clock.advance(20)
        tracker.record("svc-a", predicted=200.0, actual=100.0)

        # svc-b is accurate
        tracker.record("svc-b", predicted=100.0, actual=100.0)

        assert tracker.is_fallback_active("svc-a")
        assert not tracker.is_fallback_active("svc-b")


# ── window cap ─────────────────────────────────────────────────────────────────


class TestWindowCap:
    def test_records_capped_at_window_max(self, clock: FakeClock) -> None:
        tracker = AccuracyTracker(window_max=5, clock=clock)
        for i in range(10):
            tracker.record("svc-a", predicted=float(100 + i), actual=100.0)
            clock.advance(1)
        # Internals: should only keep last 5 records
        state = tracker._services["svc-a"]
        assert len(state.records) == 5

    def test_metrics_reflect_recent_window(self, clock: FakeClock) -> None:
        tracker = AccuracyTracker(window_max=3, clock=clock)
        # Record 5 values; only last 3 kept
        tracker.record("svc-a", predicted=200.0, actual=100.0)  # evicted
        clock.advance(1)
        tracker.record("svc-a", predicted=200.0, actual=100.0)  # evicted
        clock.advance(1)
        tracker.record("svc-a", predicted=100.0, actual=100.0)  # kept
        clock.advance(1)
        tracker.record("svc-a", predicted=100.0, actual=100.0)  # kept
        clock.advance(1)
        tracker.record("svc-a", predicted=100.0, actual=100.0)  # kept

        assert tracker.mae("svc-a") == pytest.approx(0.0)
        assert tracker.mape("svc-a") == pytest.approx(0.0)


# ── reset ──────────────────────────────────────────────────────────────────────


class TestReset:
    def test_reset_clears_state(self, tracker: AccuracyTracker) -> None:
        tracker.record("svc-a", predicted=200.0, actual=100.0)
        assert tracker.mae("svc-a") > 0
        tracker.reset("svc-a")
        assert tracker.mae("svc-a") == 0.0
        assert tracker.mape("svc-a") == 0.0
        assert not tracker.is_fallback_active("svc-a")

    def test_reset_nonexistent_service(self, tracker: AccuracyTracker) -> None:
        # Should not raise
        tracker.reset("does-not-exist")


# ── edge cases ─────────────────────────────────────────────────────────────────


class TestEdgeCases:
    def test_negative_predictions(self, tracker: AccuracyTracker) -> None:
        tracker.record("svc-a", predicted=-10.0, actual=10.0)
        assert tracker.mae("svc-a") == pytest.approx(20.0)
        assert tracker.mape("svc-a") == pytest.approx(200.0)

    def test_large_values(self, tracker: AccuracyTracker) -> None:
        tracker.record("svc-a", predicted=1e9, actual=1e9 + 1e6)
        assert tracker.mae("svc-a") == pytest.approx(1e6)
        # MAPE = 1e6 / (1e9 + 1e6) * 100 ≈ 0.0999%
        assert tracker.mape("svc-a") == pytest.approx(
            1e6 / (1e9 + 1e6) * 100, rel=1e-3
        )
