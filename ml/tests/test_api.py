"""
Tests for ml/app/main.py FastAPI endpoints.

Strategy: use create_app(skip_lifespan=True) + set_forecaster/set_spot_predictor
to inject pre-trained models without triggering the full training lifespan.
This keeps tests fast and deterministic.
"""
from __future__ import annotations

import math
from datetime import datetime, timedelta, timezone

import pytest
from fastapi.testclient import TestClient

from app.forecaster import DemandForecaster
from app.main import _state, create_app, set_forecaster, set_spot_predictor
from app.spot_predictor import SpotPredictor


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

FREQ_MINUTES = 5


def _make_history(n: int = 60) -> list[dict]:
    """60 data points = 5 hours of 5-min intervals (above ensemble threshold)."""
    start = datetime(2026, 1, 1, 0, 0, 0, tzinfo=timezone.utc)
    return [
        {
            "ds": (start + timedelta(minutes=FREQ_MINUTES * i)).isoformat(),
            "y": 100.0 + 20.0 * math.sin(2 * math.pi * i / 12),
        }
        for i in range(n)
    ]


@pytest.fixture(scope="module")
def client() -> TestClient:
    """Shared TestClient for all API tests — models injected once."""
    app = create_app(skip_lifespan=True)

    forecaster = DemandForecaster()
    sp = SpotPredictor()
    sp.train(n_synthetic=1000)  # small for speed

    set_forecaster(forecaster)
    set_spot_predictor(sp)

    return TestClient(app)


@pytest.fixture
def unloaded_client() -> TestClient:
    """TestClient with no models loaded — for 503 tests.

    Function-scoped: saves and restores _state so it doesn't pollute 'client'.
    """
    saved_f = _state.forecaster
    saved_sp = _state.spot_predictor
    set_forecaster(None)   # type: ignore[arg-type]
    set_spot_predictor(None)  # type: ignore[arg-type]
    app = create_app(skip_lifespan=True)
    yield TestClient(app)
    # Restore models for subsequent tests
    set_forecaster(saved_f)
    set_spot_predictor(saved_sp)


# ---------------------------------------------------------------------------
# POST /v1/forecast — success path
# ---------------------------------------------------------------------------


class TestForecastEndpoint:

    def test_returns_200(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={"service": "api", "metric": "rps", "history": _make_history(60)},
        )
        assert resp.status_code == 200

    def test_response_has_required_fields(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={"service": "api", "metric": "rps", "history": _make_history(60)},
        )
        body = resp.json()
        assert "forecasts" in body
        assert "model_used" in body
        assert "change_percent" in body
        assert "confidence" in body

    def test_forecasts_list_not_empty(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={"service": "api", "metric": "rps", "history": _make_history(60)},
        )
        assert len(resp.json()["forecasts"]) >= 1

    def test_forecast_point_has_yhat(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={"service": "api", "metric": "rps", "history": _make_history(60)},
        )
        pt = resp.json()["forecasts"][0]
        assert "yhat" in pt
        assert isinstance(pt["yhat"], float)

    def test_confidence_in_bounds(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={"service": "api", "metric": "rps", "history": _make_history(60)},
        )
        c = resp.json()["confidence"]
        assert 0.0 <= c <= 1.0

    def test_custom_horizons(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={
                "service": "api",
                "metric": "rps",
                "history": _make_history(60),
                "horizons_minutes": [15, 60],
            },
        )
        assert resp.status_code == 200
        assert len(resp.json()["forecasts"]) >= 1

    def test_service_and_metric_echoed(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={"service": "checkout", "metric": "latency_ms", "history": _make_history(60)},
        )
        body = resp.json()
        assert body["service"] == "checkout"
        assert body["metric"] == "latency_ms"

    def test_short_history_uses_fallback(self, client: TestClient):
        """< 24 points → SeasonalNaive model_used."""
        resp = client.post(
            "/v1/forecast",
            json={"service": "api", "metric": "rps", "history": _make_history(10)},
        )
        assert resp.status_code == 200
        assert resp.json()["model_used"] == "SeasonalNaive"

    def test_single_point_history(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={
                "service": "api",
                "metric": "rps",
                "history": [{"ds": "2026-01-01T00:00:00Z", "y": 42.0}],
            },
        )
        assert resp.status_code == 200


# ---------------------------------------------------------------------------
# POST /v1/forecast — validation errors
# ---------------------------------------------------------------------------


class TestForecastValidation:

    def test_empty_history_422(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={"service": "api", "metric": "rps", "history": []},
        )
        assert resp.status_code == 422

    def test_missing_service_422(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={"metric": "rps", "history": _make_history(5)},
        )
        assert resp.status_code == 422

    def test_invalid_horizon_422(self, client: TestClient):
        resp = client.post(
            "/v1/forecast",
            json={
                "service": "api",
                "metric": "rps",
                "history": _make_history(5),
                "horizons_minutes": [0],
            },
        )
        assert resp.status_code == 422


# ---------------------------------------------------------------------------
# POST /v1/forecast — 503 when models not loaded
# ---------------------------------------------------------------------------


class TestForecastUnloaded:

    def test_503_when_no_models(self, unloaded_client: TestClient):
        resp = unloaded_client.post(
            "/v1/forecast",
            json={"service": "api", "metric": "rps", "history": _make_history(10)},
        )
        assert resp.status_code == 503


# ---------------------------------------------------------------------------
# POST /v1/spot-risk — success path
# ---------------------------------------------------------------------------


class TestSpotRiskEndpoint:

    def test_returns_200(self, client: TestClient):
        resp = client.post(
            "/v1/spot-risk",
            json={
                "instance_type": "m5.xlarge",
                "az": "us-east-1a",
                "hour_of_day": 14,
                "day_of_week": 2,
            },
        )
        assert resp.status_code == 200

    def test_response_has_required_fields(self, client: TestClient):
        resp = client.post(
            "/v1/spot-risk",
            json={
                "instance_type": "m5.xlarge",
                "az": "us-east-1a",
                "hour_of_day": 14,
                "day_of_week": 2,
            },
        )
        body = resp.json()
        assert "interruption_probability" in body
        assert "recommended_action" in body
        assert "confidence" in body

    def test_probability_bounded(self, client: TestClient):
        resp = client.post(
            "/v1/spot-risk",
            json={
                "instance_type": "c5.large",
                "az": "us-west-2a",
                "hour_of_day": 3,
                "day_of_week": 6,
            },
        )
        p = resp.json()["interruption_probability"]
        assert 0.0 <= p <= 1.0

    def test_action_valid_literal(self, client: TestClient):
        resp = client.post(
            "/v1/spot-risk",
            json={
                "instance_type": "r5.xlarge",
                "az": "us-east-1a",
                "hour_of_day": 12,
                "day_of_week": 1,
            },
        )
        assert resp.json()["recommended_action"] in {"keep", "migrate", "switch_to_od"}

    def test_instance_az_echoed(self, client: TestClient):
        resp = client.post(
            "/v1/spot-risk",
            json={
                "instance_type": "m5.large",
                "az": "us-east-1b",
                "hour_of_day": 8,
                "day_of_week": 0,
            },
        )
        body = resp.json()
        assert body["instance_type"] == "m5.large"
        assert body["az"] == "us-east-1b"


# ---------------------------------------------------------------------------
# POST /v1/spot-risk — validation
# ---------------------------------------------------------------------------


class TestSpotRiskValidation:

    def test_invalid_hour_422(self, client: TestClient):
        resp = client.post(
            "/v1/spot-risk",
            json={
                "instance_type": "m5.large",
                "az": "us-east-1a",
                "hour_of_day": 25,
                "day_of_week": 0,
            },
        )
        assert resp.status_code == 422

    def test_invalid_day_422(self, client: TestClient):
        resp = client.post(
            "/v1/spot-risk",
            json={
                "instance_type": "m5.large",
                "az": "us-east-1a",
                "hour_of_day": 10,
                "day_of_week": 8,
            },
        )
        assert resp.status_code == 422


# ---------------------------------------------------------------------------
# POST /v1/spot-risk — 503 when models not loaded
# ---------------------------------------------------------------------------


class TestSpotRiskUnloaded:

    def test_503_when_no_models(self, unloaded_client: TestClient):
        resp = unloaded_client.post(
            "/v1/spot-risk",
            json={
                "instance_type": "m5.xlarge",
                "az": "us-east-1a",
                "hour_of_day": 10,
                "day_of_week": 1,
            },
        )
        assert resp.status_code == 503


# ---------------------------------------------------------------------------
# GET /v1/health
# ---------------------------------------------------------------------------


class TestHealthEndpoint:

    def test_returns_200_when_ready(self, client: TestClient):
        resp = client.get("/v1/health")
        assert resp.status_code == 200

    def test_models_loaded_true_when_ready(self, client: TestClient):
        resp = client.get("/v1/health")
        assert resp.json()["models_loaded"] is True

    def test_status_ok_when_ready(self, client: TestClient):
        resp = client.get("/v1/health")
        assert resp.json()["status"] == "ok"

    def test_degraded_when_no_models(self, unloaded_client: TestClient):
        resp = unloaded_client.get("/v1/health")
        assert resp.json()["status"] == "degraded"
        assert resp.json()["models_loaded"] is False


# ---------------------------------------------------------------------------
# GET /metrics
# ---------------------------------------------------------------------------


class TestMetricsEndpoint:

    def test_returns_200(self, client: TestClient):
        resp = client.get("/metrics")
        assert resp.status_code == 200

    def test_prometheus_content_type(self, client: TestClient):
        resp = client.get("/metrics")
        assert "text/plain" in resp.headers["content-type"]

    def test_contains_forecast_latency_metric(self, client: TestClient):
        # Trigger a forecast first so the histogram is populated
        client.post(
            "/v1/forecast",
            json={"service": "api", "metric": "rps", "history": _make_history(30)},
        )
        resp = client.get("/metrics")
        assert b"optipilot_forecast_latency_seconds" in resp.content
