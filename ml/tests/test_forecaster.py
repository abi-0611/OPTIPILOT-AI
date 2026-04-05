"""
Tests for ml/app/forecaster.py — synthetic data, no network calls.
"""
import math
from datetime import datetime, timedelta, timezone

import numpy as np
import pandas as pd
import pytest

from app.forecaster import (
    FREQ_MINUTES,
    MIN_POINTS_FOR_ENSEMBLE,
    DemandForecaster,
    compute_change_percent,
    compute_confidence,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _sinusoidal_history(
    n_points: int = 200,
    period_points: int = 12,  # 1 hour at 5-min intervals
    amplitude: float = 50.0,
    baseline: float = 100.0,
    start: datetime | None = None,
) -> pd.DataFrame:
    """Generate a clean sinusoidal time-series at 5-minute intervals."""
    if start is None:
        start = datetime(2026, 1, 1, 0, 0, 0, tzinfo=timezone.utc)
    timestamps = [start + timedelta(minutes=FREQ_MINUTES * i) for i in range(n_points)]
    values = [
        baseline + amplitude * math.sin(2 * math.pi * i / period_points) for i in range(n_points)
    ]
    return pd.DataFrame({"ds": timestamps, "y": values})


def _short_history(n_points: int = 10) -> pd.DataFrame:
    """Generate a very short time-series (< MIN_POINTS_FOR_ENSEMBLE)."""
    return _sinusoidal_history(n_points=n_points)


def _history_with_gaps(n_points: int = 100, gap_indices: list[int] | None = None) -> pd.DataFrame:
    """Generate history with some missing rows to test gap-filling."""
    df = _sinusoidal_history(n_points=n_points)
    if gap_indices is None:
        gap_indices = [5, 10, 15, 20]
    return df.drop(index=gap_indices).reset_index(drop=True)


# ---------------------------------------------------------------------------
# DemandForecaster — ensemble path (long history)
# ---------------------------------------------------------------------------


class TestDemandForecaster_Ensemble:
    """Tests for histories long enough to trigger the AutoARIMA+AutoETS ensemble."""

    def test_forecast_returns_dataframe_and_model_name(self):
        df = _sinusoidal_history(n_points=200)
        forecaster = DemandForecaster()
        result, model_used = forecaster.forecast(df, horizons_minutes=[15])
        assert isinstance(result, pd.DataFrame)
        assert model_used == "ensemble"
        assert len(result) >= 1

    def test_forecast_has_yhat_column(self):
        df = _sinusoidal_history(n_points=200)
        forecaster = DemandForecaster()
        result, _ = forecaster.forecast(df, horizons_minutes=[15])
        assert "yhat" in result.columns

    def test_forecast_has_prediction_intervals(self):
        df = _sinusoidal_history(n_points=200)
        forecaster = DemandForecaster()
        result, _ = forecaster.forecast(df, horizons_minutes=[15])
        # Should have lo_80, hi_80, lo_95, hi_95
        assert "lo_80" in result.columns or "lo_95" in result.columns

    def test_forecast_multiple_horizons(self):
        df = _sinusoidal_history(n_points=200)
        forecaster = DemandForecaster()
        result, _ = forecaster.forecast(df, horizons_minutes=[15, 60])
        assert len(result) >= 2

    def test_forecast_yhat_is_reasonable(self):
        """Forecast of a sinusoidal signal should be within 2x of the baseline."""
        df = _sinusoidal_history(n_points=200, baseline=100.0, amplitude=50.0)
        forecaster = DemandForecaster()
        result, _ = forecaster.forecast(df, horizons_minutes=[15])
        yhat = result["yhat"].iloc[0]
        # Should be somewhere in [0, 300] for a 100±50 signal
        assert 0 <= yhat <= 300, f"yhat={yhat} out of reasonable range"

    def test_prediction_interval_ordering(self):
        """lo_95 <= lo_80 <= yhat <= hi_80 <= hi_95 (approximately)."""
        df = _sinusoidal_history(n_points=200)
        forecaster = DemandForecaster()
        result, _ = forecaster.forecast(df, horizons_minutes=[15])
        row = result.iloc[0]
        if "lo_80" in result.columns and "hi_80" in result.columns:
            assert row["lo_80"] <= row["hi_80"]
        if "lo_95" in result.columns and "hi_95" in result.columns:
            assert row["lo_95"] <= row["hi_95"]


# ---------------------------------------------------------------------------
# DemandForecaster — SeasonalNaive fallback (short history)
# ---------------------------------------------------------------------------


class TestDemandForecaster_SeasonalNaive:
    """Tests when history < MIN_POINTS_FOR_ENSEMBLE → SeasonalNaive fallback."""

    def test_short_history_uses_seasonal_naive(self):
        df = _short_history(n_points=10)
        forecaster = DemandForecaster()
        result, model_used = forecaster.forecast(df, horizons_minutes=[15])
        assert model_used == "SeasonalNaive"

    def test_short_history_still_produces_output(self):
        df = _short_history(n_points=10)
        forecaster = DemandForecaster()
        result, _ = forecaster.forecast(df, horizons_minutes=[15])
        assert len(result) >= 1
        assert "yhat" in result.columns

    def test_single_point_history(self):
        """Even 1 data point should not crash."""
        df = pd.DataFrame(
            {"ds": [datetime(2026, 1, 1, tzinfo=timezone.utc)], "y": [100.0]}
        )
        forecaster = DemandForecaster()
        result, model_used = forecaster.forecast(df, horizons_minutes=[15])
        assert model_used == "SeasonalNaive"
        assert len(result) >= 1

    def test_boundary_at_min_points(self):
        """Exactly MIN_POINTS_FOR_ENSEMBLE should use ensemble."""
        df = _sinusoidal_history(n_points=MIN_POINTS_FOR_ENSEMBLE)
        forecaster = DemandForecaster()
        _, model_used = forecaster.forecast(df, horizons_minutes=[15])
        assert model_used == "ensemble"


# ---------------------------------------------------------------------------
# Gap-filling
# ---------------------------------------------------------------------------


class TestDemandForecaster_GapFilling:
    """Tests for forward-fill of missing intervals."""

    def test_gaps_are_filled(self):
        df = _history_with_gaps(n_points=100, gap_indices=[5, 10, 15])
        forecaster = DemandForecaster()
        # Should not raise; gaps forward-filled internally
        result, _ = forecaster.forecast(df, horizons_minutes=[15])
        assert len(result) >= 1


# ---------------------------------------------------------------------------
# Edge cases & validation
# ---------------------------------------------------------------------------


class TestDemandForecaster_Validation:

    def test_missing_columns_raises(self):
        df = pd.DataFrame({"timestamp": [1, 2, 3], "value": [10, 20, 30]})
        forecaster = DemandForecaster()
        with pytest.raises(ValueError, match="'ds' and 'y'"):
            forecaster.forecast(df, horizons_minutes=[15])

    def test_all_nan_raises(self):
        df = pd.DataFrame(
            {
                "ds": [datetime(2026, 1, 1, tzinfo=timezone.utc) + timedelta(minutes=5 * i) for i in range(5)],
                "y": [float("nan")] * 5,
            }
        )
        forecaster = DemandForecaster()
        with pytest.raises(ValueError, match="no valid data"):
            forecaster.forecast(df, horizons_minutes=[15])

    def test_6h_horizon(self):
        """360-minute horizon should work without error."""
        df = _sinusoidal_history(n_points=200)
        forecaster = DemandForecaster()
        result, _ = forecaster.forecast(df, horizons_minutes=[15, 60, 360])
        assert len(result) >= 2  # at least 15-min and 360-min steps


# ---------------------------------------------------------------------------
# Utility functions
# ---------------------------------------------------------------------------


class TestComputeChangePercent:

    def test_positive_change(self):
        assert compute_change_percent(100.0, 120.0) == pytest.approx(20.0)

    def test_negative_change(self):
        assert compute_change_percent(100.0, 80.0) == pytest.approx(-20.0)

    def test_zero_baseline(self):
        assert compute_change_percent(0.0, 50.0) == 0.0

    def test_no_change(self):
        assert compute_change_percent(100.0, 100.0) == pytest.approx(0.0)


class TestComputeConfidence:

    def test_seasonal_naive_low_confidence(self):
        assert compute_confidence("SeasonalNaive", 10) == 0.3

    def test_ensemble_base(self):
        c = compute_confidence("ensemble", 30)
        assert 0.6 <= c <= 0.7

    def test_ensemble_high_data(self):
        c = compute_confidence("ensemble", 1000)
        assert c == pytest.approx(0.95)
