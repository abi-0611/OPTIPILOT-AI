"""
Demand forecaster using statsforecast (AutoARIMA + AutoETS ensemble).

Handles:
- Normal history (>=24 points / 2 hours at 5-min intervals): ensemble of AutoARIMA + AutoETS
- Short history (<24 points): fallback to SeasonalNaive
- Missing data points: forward-fill gaps before fitting
"""
from __future__ import annotations

import logging
from datetime import datetime, timedelta, timezone
from typing import TYPE_CHECKING

import numpy as np
import pandas as pd
from statsforecast import StatsForecast
from statsforecast.models import AutoARIMA, AutoETS, SeasonalNaive

if TYPE_CHECKING:
    pass

logger = logging.getLogger(__name__)

# 5-minute sampling interval → 12 points/hour → season_length = 12 (hourly seasonality)
FREQ_MINUTES = 5
SEASON_LENGTH = 12  # 1 hour at 5-min intervals
MIN_POINTS_FOR_ENSEMBLE = 24  # 2 hours of data
CONFIDENCE_LEVELS = [80, 95]

# Column name used by statsforecast
_UNIQUE_ID = "ts"


class DemandForecaster:
    """Stateless demand forecaster — fits on each call (no persistent model state).

    This is intentional: forecasts are generated per-service with varying
    history windows, so a single pre-trained model doesn't make sense.
    """

    def forecast(
        self,
        history: pd.DataFrame,
        horizons_minutes: list[int],
    ) -> tuple[pd.DataFrame, str]:
        """Produce point forecasts + prediction intervals.

        Args:
            history: DataFrame with columns ``ds`` (datetime) and ``y`` (float).
            horizons_minutes: list of forecast horizons in minutes (e.g. [15, 60, 360]).

        Returns:
            Tuple of (forecast_df, model_used).
            ``forecast_df`` has columns: ``ds``, ``yhat``, and for each level
            ``lo_{level}``, ``hi_{level}``.
        """
        df = self._prepare(history)
        n_points = len(df)
        max_horizon_steps = max(h // FREQ_MINUTES for h in horizons_minutes)
        # Ensure at least 1 step
        max_horizon_steps = max(max_horizon_steps, 1)

        model_used, sf = self._build_model(n_points)

        # Fit and predict — statsforecast v2 requires df passed to forecast()
        sf_result = sf.forecast(df=df, h=max_horizon_steps, level=CONFIDENCE_LEVELS)

        # Build output
        result = self._format_output(sf_result, horizons_minutes, model_used)
        return result, model_used

    # ── internal helpers ──────────────────────────────────────────────────

    def _prepare(self, history: pd.DataFrame) -> pd.DataFrame:
        """Validate, sort, forward-fill gaps, add unique_id column."""
        df = history.copy()
        if "ds" not in df.columns or "y" not in df.columns:
            raise ValueError("history must have 'ds' and 'y' columns")

        # Ensure datetime
        df["ds"] = pd.to_datetime(df["ds"], utc=True)
        df = df.sort_values("ds").reset_index(drop=True)

        # Drop NaN y values before gap-filling
        df = df.dropna(subset=["y"])
        if len(df) == 0:
            raise ValueError("history contains no valid data points after NaN removal")

        # Forward-fill missing 5-minute intervals
        df = self._fill_gaps(df)

        # statsforecast requires a 'unique_id' column
        df["unique_id"] = _UNIQUE_ID
        return df

    @staticmethod
    def _fill_gaps(df: pd.DataFrame) -> pd.DataFrame:
        """Resample to strict 5-minute intervals and forward-fill missing values."""
        if len(df) < 2:
            return df

        start = df["ds"].iloc[0]
        end = df["ds"].iloc[-1]
        full_idx = pd.date_range(start=start, end=end, freq=f"{FREQ_MINUTES}min", tz=timezone.utc)

        df = df.set_index("ds").reindex(full_idx)
        df["y"] = df["y"].ffill()
        df = df.reset_index().rename(columns={"index": "ds"})
        return df

    @staticmethod
    def _build_model(n_points: int) -> tuple[str, StatsForecast]:
        """Choose model(s) based on history length."""
        if n_points < MIN_POINTS_FOR_ENSEMBLE:
            logger.info("Short history (%d points) — using SeasonalNaive fallback", n_points)
            models = [SeasonalNaive(season_length=SEASON_LENGTH)]
            model_name = "SeasonalNaive"
        else:
            models = [
                AutoARIMA(season_length=SEASON_LENGTH),
                AutoETS(season_length=SEASON_LENGTH),
            ]
            model_name = "ensemble"

        sf = StatsForecast(models=models, freq=f"{FREQ_MINUTES}min", n_jobs=1)
        return model_name, sf

    def _format_output(
        self,
        sf_result: pd.DataFrame,
        horizons_minutes: list[int],
        model_used: str,
    ) -> pd.DataFrame:
        """Extract requested horizon steps from the full forecast grid."""
        # sf_result index: 0…h-1 (step index).  Columns depend on model(s).
        # Reset any multi-index; drop=True prevents RangeIndex becoming an 'index' column.
        sf_result = sf_result.reset_index(drop=True)

        # Map horizon minutes → step indices
        steps = sorted(set(max(h // FREQ_MINUTES - 1, 0) for h in horizons_minutes))

        rows: list[dict] = []
        for step in steps:
            if step >= len(sf_result):
                step = len(sf_result) - 1

            row_data = sf_result.iloc[step]
            out: dict = {"ds": str(row_data["ds"])}

            # Point forecast: average ensemble members (if >1 model column)
            yhat_cols = self._yhat_columns(sf_result, model_used)
            yhat_vals = [float(row_data[c]) for c in yhat_cols if c in sf_result.columns]
            out["yhat"] = float(np.mean(yhat_vals)) if yhat_vals else 0.0

            # Prediction intervals
            for level in CONFIDENCE_LEVELS:
                lo_cols = [c for c in sf_result.columns if f"-lo-{level}" in c]
                hi_cols = [c for c in sf_result.columns if f"-hi-{level}" in c]
                if lo_cols:
                    lo_vals = [float(row_data[c]) for c in lo_cols]
                    out[f"lo_{level}"] = float(np.mean(lo_vals))
                if hi_cols:
                    hi_vals = [float(row_data[c]) for c in hi_cols]
                    out[f"hi_{level}"] = float(np.mean(hi_vals))

            rows.append(out)

        return pd.DataFrame(rows)

    @staticmethod
    def _yhat_columns(sf_result: pd.DataFrame, model_used: str) -> list[str]:
        """Identify the point-forecast columns (not lo/hi/ds/unique_id)."""
        reserved = {"ds", "unique_id"}
        return [
            c
            for c in sf_result.columns
            if c not in reserved and "-lo-" not in c and "-hi-" not in c
        ]


def compute_change_percent(history_tail_y: float, forecast_yhat: float) -> float:
    """Compute the % change of the 15-min forecast vs the latest actual value."""
    if abs(history_tail_y) < 1e-9:
        return 0.0
    return ((forecast_yhat - history_tail_y) / abs(history_tail_y)) * 100.0


def compute_confidence(model_used: str, n_points: int) -> float:
    """Heuristic confidence score in [0, 1]."""
    if model_used == "SeasonalNaive":
        return 0.3
    # More data → higher confidence, capped at 0.95
    base = 0.6
    bonus = min(n_points / 500.0, 0.35)  # up to 0.35 bonus for 500+ points
    return min(base + bonus, 0.95)
