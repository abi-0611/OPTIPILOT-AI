"""
Forecast accuracy tracker.

After each forecast horizon passes (e.g. 15 minutes later), the caller
records the predicted value and the actual observed value.  The tracker
computes MAE and MAPE over a sliding window and, if MAPE exceeds 30 %
for a sustained period (default 1 hour), marks that service as
"fallback active" — meaning the solver should skip pre-warming.

Prometheus metrics exposed:
  - optipilot_forecast_mae        (Gauge, per service)
  - optipilot_forecast_mape       (Gauge, per service)
  - optipilot_forecast_fallback_active (Gauge, per service)
"""
from __future__ import annotations

import logging
import time
from collections import defaultdict
from dataclasses import dataclass, field

logger = logging.getLogger(__name__)

# ── thresholds ────────────────────────────────────────────────────────────────

MAPE_THRESHOLD = 30.0  # percent
FALLBACK_SUSTAIN_SECONDS = 3600.0  # 1 hour of sustained high MAPE → disable
WINDOW_MAX_RECORDS = 200  # rolling window cap per service


@dataclass
class _Record:
    """A single predicted-vs-actual pair."""

    ts: float  # epoch seconds
    predicted: float
    actual: float


@dataclass
class _ServiceState:
    """Per-service accuracy tracking state."""

    records: list[_Record] = field(default_factory=list)
    mae: float = 0.0
    mape: float = 0.0
    fallback_active: bool = False
    # Timestamp when MAPE first exceeded the threshold (0 = not exceeded)
    high_mape_since: float = 0.0


class AccuracyTracker:
    """Tracks forecast accuracy per service and decides fallback status."""

    def __init__(
        self,
        *,
        mape_threshold: float = MAPE_THRESHOLD,
        sustain_seconds: float = FALLBACK_SUSTAIN_SECONDS,
        window_max: int = WINDOW_MAX_RECORDS,
        clock: object | None = None,
    ) -> None:
        self._threshold = mape_threshold
        self._sustain = sustain_seconds
        self._window_max = window_max
        self._services: dict[str, _ServiceState] = defaultdict(_ServiceState)
        self._clock = clock or time  # injectable for tests

    # ── public API ────────────────────────────────────────────────────────

    def record(self, service: str, predicted: float, actual: float) -> None:
        """Record a predicted-vs-actual pair and recompute metrics."""
        now = self._clock.time()
        state = self._services[service]

        state.records.append(_Record(ts=now, predicted=predicted, actual=actual))

        # Evict oldest records if over window cap.
        if len(state.records) > self._window_max:
            state.records = state.records[-self._window_max :]

        self._recompute(service, now)

    def mae(self, service: str) -> float:
        """Return the latest MAE for *service* (0.0 if no data)."""
        return self._services[service].mae

    def mape(self, service: str) -> float:
        """Return the latest MAPE (%) for *service* (0.0 if no data)."""
        return self._services[service].mape

    def is_fallback_active(self, service: str) -> bool:
        """True if pre-warming should be disabled for *service*."""
        return self._services[service].fallback_active

    def services(self) -> list[str]:
        """Return all tracked service names."""
        return list(self._services.keys())

    def reset(self, service: str) -> None:
        """Clear all records for a service and deactivate fallback."""
        if service in self._services:
            del self._services[service]

    # ── internals ─────────────────────────────────────────────────────────

    def _recompute(self, service: str, now: float) -> None:
        state = self._services[service]
        records = state.records
        if not records:
            state.mae = 0.0
            state.mape = 0.0
            state.fallback_active = False
            state.high_mape_since = 0.0
            return

        total_ae = 0.0
        total_ape = 0.0
        ape_count = 0

        for r in records:
            ae = abs(r.predicted - r.actual)
            total_ae += ae
            if r.actual != 0.0:
                total_ape += ae / abs(r.actual) * 100.0
                ape_count += 1

        n = len(records)
        state.mae = total_ae / n
        state.mape = total_ape / ape_count if ape_count > 0 else 0.0

        # Fallback logic: MAPE above threshold for sustained period?
        if state.mape > self._threshold:
            if state.high_mape_since == 0.0:
                state.high_mape_since = now
            elapsed = now - state.high_mape_since
            if elapsed >= self._sustain:
                if not state.fallback_active:
                    logger.warning(
                        "Forecast accuracy degraded for %s: MAPE=%.1f%% for %.0fs — "
                        "disabling pre-warming",
                        service,
                        state.mape,
                        elapsed,
                    )
                state.fallback_active = True
        else:
            # MAPE dropped below threshold → reset timer and re-enable.
            state.high_mape_since = 0.0
            if state.fallback_active:
                logger.info(
                    "Forecast accuracy recovered for %s: MAPE=%.1f%% — re-enabling pre-warming",
                    service,
                    state.mape,
                )
            state.fallback_active = False
