"""
Pydantic request/response schemas for the OptiPilot ML service.

All timestamps use ISO-8601 strings (UTC) for portability with Go's time.Time.
"""
from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field, field_validator


# ---------------------------------------------------------------------------
# Shared primitives
# ---------------------------------------------------------------------------


class MetricPoint(BaseModel):
    """A single (timestamp, value) observation in a time-series."""

    ds: str = Field(..., description="ISO-8601 UTC timestamp, e.g. '2026-01-01T00:00:00Z'")
    y: float = Field(..., description="Observed metric value (e.g. RPS, latency-ms)")


class PredictionInterval(BaseModel):
    """Symmetric prediction interval at a given confidence level."""

    level: int = Field(..., ge=1, le=99, description="Confidence level (e.g. 80 or 95)")
    lower: float
    upper: float


class ForecastPoint(BaseModel):
    """One forecast horizon step: point estimate + intervals."""

    ds: str = Field(..., description="ISO-8601 UTC timestamp of the forecast horizon")
    yhat: float = Field(..., description="Point forecast")
    intervals: list[PredictionInterval] = Field(default_factory=list)


# ---------------------------------------------------------------------------
# Demand forecast
# ---------------------------------------------------------------------------


class DemandForecastRequest(BaseModel):
    """Historical metric time-series → multi-horizon demand forecast."""

    service: str = Field(..., min_length=1, description="Service name (for logging/accuracy tracking)")
    metric: str = Field(..., min_length=1, description="Metric name, e.g. 'rps' or 'latency_ms'")
    history: list[MetricPoint] = Field(
        ...,
        min_length=1,
        description="Time-ordered observations (oldest first). Must have at least 1 point.",
    )
    horizons_minutes: list[int] = Field(
        default=[15, 60, 360],
        description="Forecast horizons in minutes (e.g. 15, 60, 360).",
    )

    @field_validator("horizons_minutes")
    @classmethod
    def horizons_positive(cls, v: list[int]) -> list[int]:
        if any(h <= 0 for h in v):
            raise ValueError("All horizons must be positive integers")
        return v


class DemandForecastResponse(BaseModel):
    """Multi-horizon forecast result with model metadata."""

    service: str
    metric: str
    forecasts: list[ForecastPoint] = Field(
        ..., description="One entry per requested horizon step"
    )
    model_used: str = Field(..., description="'ensemble', 'AutoARIMA', 'AutoETS', or 'SeasonalNaive'")
    change_percent: float = Field(
        ...,
        description="Expected % change in the next 15-minute horizon vs current tail of history",
    )
    confidence: float = Field(
        ..., ge=0.0, le=1.0, description="Model confidence score in [0, 1]"
    )
    fallback_active: bool = Field(
        default=False, description="True when accuracy tracking has disabled this model"
    )


# ---------------------------------------------------------------------------
# Spot risk prediction
# ---------------------------------------------------------------------------

SpotAction = Literal["keep", "migrate", "switch_to_od"]


class SpotRiskRequest(BaseModel):
    """Features for a spot interruption probability estimate."""

    instance_type: str = Field(..., min_length=1, description="e.g. 'm5.xlarge'")
    az: str = Field(..., min_length=1, description="Availability zone, e.g. 'us-east-1a'")
    hour_of_day: int = Field(..., ge=0, le=23)
    day_of_week: int = Field(..., ge=0, le=6, description="0=Monday … 6=Sunday")
    recent_interruption_count_7d: int = Field(
        default=0, ge=0, description="Number of interruptions in the past 7 days"
    )
    spot_price_ratio: float = Field(
        default=1.0,
        gt=0.0,
        description="spot_price / on_demand_price; >1.0 means spot is more expensive",
    )


class SpotRiskResponse(BaseModel):
    """Predicted spot interruption risk and recommended action."""

    instance_type: str
    az: str
    interruption_probability: float = Field(..., ge=0.0, le=1.0)
    recommended_action: SpotAction
    confidence: float = Field(..., ge=0.0, le=1.0)


# ---------------------------------------------------------------------------
# Health / metrics
# ---------------------------------------------------------------------------


class HealthResponse(BaseModel):
    status: Literal["ok", "degraded"] = "ok"
    models_loaded: bool
    forecaster_ready: bool
    spot_predictor_ready: bool
