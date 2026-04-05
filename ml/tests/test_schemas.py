"""
Tests for ml/app/schemas.py — pure Pydantic validation, no network calls.
"""
import pytest
from pydantic import ValidationError

from app.schemas import (
    DemandForecastRequest,
    ForecastPoint,
    HealthResponse,
    MetricPoint,
    PredictionInterval,
    SpotRiskRequest,
    SpotRiskResponse,
)


# ---------------------------------------------------------------------------
# MetricPoint
# ---------------------------------------------------------------------------


def test_metric_point_valid():
    p = MetricPoint(ds="2026-01-01T00:00:00Z", y=42.0)
    assert p.y == 42.0


def test_metric_point_negative_y_allowed():
    # Negative latency deltas are valid (e.g. improvement)
    p = MetricPoint(ds="2026-01-01T00:00:00Z", y=-1.5)
    assert p.y == -1.5


# ---------------------------------------------------------------------------
# DemandForecastRequest
# ---------------------------------------------------------------------------


def test_forecast_request_defaults():
    req = DemandForecastRequest(
        service="api",
        metric="rps",
        history=[{"ds": "2026-01-01T00:00:00Z", "y": 100.0}],
    )
    assert req.horizons_minutes == [15, 60, 360]


def test_forecast_request_custom_horizons():
    req = DemandForecastRequest(
        service="api",
        metric="rps",
        history=[{"ds": "2026-01-01T00:00:00Z", "y": 100.0}],
        horizons_minutes=[15, 30],
    )
    assert 30 in req.horizons_minutes


def test_forecast_request_empty_history_fails():
    with pytest.raises(ValidationError):
        DemandForecastRequest(service="api", metric="rps", history=[])


def test_forecast_request_empty_service_fails():
    with pytest.raises(ValidationError):
        DemandForecastRequest(
            service="",
            metric="rps",
            history=[{"ds": "2026-01-01T00:00:00Z", "y": 1.0}],
        )


def test_forecast_request_zero_horizon_fails():
    with pytest.raises(ValidationError):
        DemandForecastRequest(
            service="api",
            metric="rps",
            history=[{"ds": "2026-01-01T00:00:00Z", "y": 1.0}],
            horizons_minutes=[0, 60],
        )


def test_forecast_request_negative_horizon_fails():
    with pytest.raises(ValidationError):
        DemandForecastRequest(
            service="api",
            metric="rps",
            history=[{"ds": "2026-01-01T00:00:00Z", "y": 1.0}],
            horizons_minutes=[-15],
        )


# ---------------------------------------------------------------------------
# PredictionInterval
# ---------------------------------------------------------------------------


def test_prediction_interval_valid():
    pi = PredictionInterval(level=95, lower=80.0, upper=120.0)
    assert pi.level == 95


def test_prediction_interval_level_out_of_range():
    with pytest.raises(ValidationError):
        PredictionInterval(level=100, lower=0.0, upper=1.0)


def test_prediction_interval_level_zero():
    with pytest.raises(ValidationError):
        PredictionInterval(level=0, lower=0.0, upper=1.0)


# ---------------------------------------------------------------------------
# ForecastPoint
# ---------------------------------------------------------------------------


def test_forecast_point_no_intervals():
    fp = ForecastPoint(ds="2026-01-01T00:15:00Z", yhat=105.3)
    assert fp.intervals == []


def test_forecast_point_with_intervals():
    fp = ForecastPoint(
        ds="2026-01-01T00:15:00Z",
        yhat=105.3,
        intervals=[{"level": 80, "lower": 95.0, "upper": 115.0}],
    )
    assert len(fp.intervals) == 1
    assert fp.intervals[0].lower == 95.0


# ---------------------------------------------------------------------------
# SpotRiskRequest
# ---------------------------------------------------------------------------


def test_spot_risk_request_defaults():
    req = SpotRiskRequest(
        instance_type="m5.xlarge",
        az="us-east-1a",
        hour_of_day=14,
        day_of_week=2,
    )
    assert req.recent_interruption_count_7d == 0
    assert req.spot_price_ratio == 1.0


def test_spot_risk_request_invalid_hour():
    with pytest.raises(ValidationError):
        SpotRiskRequest(
            instance_type="m5.xlarge",
            az="us-east-1a",
            hour_of_day=24,
            day_of_week=0,
        )


def test_spot_risk_request_invalid_day():
    with pytest.raises(ValidationError):
        SpotRiskRequest(
            instance_type="m5.xlarge",
            az="us-east-1a",
            hour_of_day=10,
            day_of_week=7,
        )


def test_spot_risk_request_negative_price_ratio():
    with pytest.raises(ValidationError):
        SpotRiskRequest(
            instance_type="m5.xlarge",
            az="us-east-1a",
            hour_of_day=10,
            day_of_week=1,
            spot_price_ratio=-0.1,
        )


def test_spot_risk_request_zero_price_ratio():
    with pytest.raises(ValidationError):
        SpotRiskRequest(
            instance_type="m5.xlarge",
            az="us-east-1a",
            hour_of_day=10,
            day_of_week=1,
            spot_price_ratio=0.0,
        )


# ---------------------------------------------------------------------------
# SpotRiskResponse
# ---------------------------------------------------------------------------


def test_spot_risk_response_valid():
    r = SpotRiskResponse(
        instance_type="m5.xlarge",
        az="us-east-1a",
        interruption_probability=0.35,
        recommended_action="keep",
        confidence=0.8,
    )
    assert r.recommended_action == "keep"


def test_spot_risk_response_probability_bounds():
    with pytest.raises(ValidationError):
        SpotRiskResponse(
            instance_type="m5.xlarge",
            az="us-east-1a",
            interruption_probability=1.5,
            recommended_action="keep",
            confidence=0.8,
        )


def test_spot_risk_response_invalid_action():
    with pytest.raises(ValidationError):
        SpotRiskResponse(
            instance_type="m5.xlarge",
            az="us-east-1a",
            interruption_probability=0.5,
            recommended_action="reboot",
            confidence=0.8,
        )


# ---------------------------------------------------------------------------
# HealthResponse
# ---------------------------------------------------------------------------


def test_health_response_defaults():
    h = HealthResponse(models_loaded=True, forecaster_ready=True, spot_predictor_ready=True)
    assert h.status == "ok"


def test_health_response_degraded():
    h = HealthResponse(
        status="degraded",
        models_loaded=False,
        forecaster_ready=False,
        spot_predictor_ready=True,
    )
    assert not h.models_loaded


def test_health_response_invalid_status():
    with pytest.raises(ValidationError):
        HealthResponse(
            status="error",
            models_loaded=False,
            forecaster_ready=False,
            spot_predictor_ready=False,
        )
