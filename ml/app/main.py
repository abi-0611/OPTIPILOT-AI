"""
OptiPilot ML Service — FastAPI application.

Endpoints:
  POST /v1/forecast              DemandForecastRequest → DemandForecastResponse
  POST /v1/spot-risk             SpotRiskRequest       → SpotRiskResponse
  GET  /v1/health                HealthResponse
  GET  /metrics                  Prometheus text exposition
"""
from __future__ import annotations

import asyncio
import logging
import os
import time
from contextlib import asynccontextmanager
from typing import Any

import pandas as pd
from fastapi import FastAPI, Request, Response, status
from fastapi.responses import JSONResponse, PlainTextResponse
from prometheus_client import (
    CONTENT_TYPE_LATEST,
    Counter,
    Gauge,
    Histogram,
    generate_latest,
)

from app.forecaster import (
    DemandForecaster,
    compute_change_percent,
    compute_confidence,
)
from app.schemas import (
    DemandForecastRequest,
    DemandForecastResponse,
    ForecastPoint,
    HealthResponse,
    PredictionInterval,
    SpotRiskRequest,
    SpotRiskResponse,
)
from app.spot_predictor import SpotPredictor

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Prometheus metrics
# ---------------------------------------------------------------------------

_forecast_latency = Histogram(
    "optipilot_forecast_latency_seconds",
    "Time to produce a demand forecast",
    buckets=(0.05, 0.1, 0.5, 1.0, 2.0, 5.0, 10.0),
)
_forecast_requests = Counter(
    "optipilot_forecast_requests_total",
    "Total demand forecast requests",
    ["status"],
)
_spot_risk_latency = Histogram(
    "optipilot_spot_risk_latency_seconds",
    "Time to produce a spot risk prediction",
    buckets=(0.01, 0.05, 0.1, 0.5, 1.0),
)
_spot_risk_requests = Counter(
    "optipilot_spot_risk_requests_total",
    "Total spot risk requests",
    ["status"],
)
_forecast_mae = Gauge("optipilot_forecast_mae", "Latest forecast MAE (filled by accuracy tracker)")
_forecast_mape = Gauge(
    "optipilot_forecast_mape", "Latest forecast MAPE (filled by accuracy tracker)"
)
_forecast_fallback_active = Gauge(
    "optipilot_forecast_fallback_active",
    "1 if fallback is active for any service",
)

# ---------------------------------------------------------------------------
# Application state holder
# ---------------------------------------------------------------------------


class _AppState:
    forecaster: DemandForecaster | None = None
    spot_predictor: SpotPredictor | None = None

    @property
    def ready(self) -> bool:
        return self.forecaster is not None and self.spot_predictor is not None


_state = _AppState()

# ---------------------------------------------------------------------------
# Lifespan: boot models
# ---------------------------------------------------------------------------

_REQUEST_TIMEOUT_SECONDS = 10.0


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Load / train models at startup."""
    logger.info("OptiPilot ML: loading models …")
    _state.forecaster = DemandForecaster()

    sp = SpotPredictor()
    model_path = os.environ.get("SPOT_MODEL_PATH", "")
    if model_path and os.path.exists(model_path):
        sp.load(model_path)
        logger.info("SpotPredictor loaded from %s", model_path)
    else:
        logger.info("No SPOT_MODEL_PATH set — training on synthetic data …")
        # Offload to thread pool so we don't block the event loop
        loop = asyncio.get_event_loop()
        await loop.run_in_executor(None, lambda: sp.train())

    _state.spot_predictor = sp
    logger.info("OptiPilot ML: models ready")
    yield
    logger.info("OptiPilot ML: shutting down")


# ---------------------------------------------------------------------------
# App factory
# ---------------------------------------------------------------------------


def create_app(*, skip_lifespan: bool = False) -> FastAPI:
    """Create the FastAPI application.

    ``skip_lifespan=True`` is used in tests to pre-inject models without
    triggering the full training lifespan.
    """
    _lifespan = None if skip_lifespan else lifespan
    app = FastAPI(
        title="OptiPilot ML Service",
        version="0.1.0",
        lifespan=_lifespan,
    )
    _register_routes(app)
    return app


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------


def _register_routes(app: FastAPI) -> None:

    @app.post(
        "/v1/forecast",
        response_model=DemandForecastResponse,
        status_code=status.HTTP_200_OK,
    )
    async def forecast(req: DemandForecastRequest, request: Request) -> DemandForecastResponse:
        if not _state.ready:
            _forecast_requests.labels(status="error").inc()
            return JSONResponse(
                status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
                content={"detail": "models not loaded"},
            )

        t0 = time.perf_counter()
        try:
            result = await asyncio.wait_for(
                _run_forecast(req),
                timeout=_REQUEST_TIMEOUT_SECONDS,
            )
            _forecast_requests.labels(status="ok").inc()
            return result
        except asyncio.TimeoutError:
            _forecast_requests.labels(status="timeout").inc()
            return JSONResponse(
                status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
                content={"detail": "forecast timed out"},
            )
        except Exception as exc:  # noqa: BLE001
            logger.exception("Forecast error: %s", exc)
            _forecast_requests.labels(status="error").inc()
            return JSONResponse(
                status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
                content={"detail": str(exc)},
            )
        finally:
            _forecast_latency.observe(time.perf_counter() - t0)

    @app.post(
        "/v1/spot-risk",
        response_model=SpotRiskResponse,
        status_code=status.HTTP_200_OK,
    )
    async def spot_risk(req: SpotRiskRequest) -> SpotRiskResponse:
        if not _state.ready:
            _spot_risk_requests.labels(status="error").inc()
            return JSONResponse(
                status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
                content={"detail": "models not loaded"},
            )

        t0 = time.perf_counter()
        try:
            loop = asyncio.get_event_loop()
            pred = await loop.run_in_executor(
                None,
                lambda: _state.spot_predictor.predict(  # type: ignore[union-attr]
                    instance_type=req.instance_type,
                    az=req.az,
                    hour_of_day=req.hour_of_day,
                    day_of_week=req.day_of_week,
                    recent_interruption_count_7d=req.recent_interruption_count_7d,
                    spot_price_ratio=req.spot_price_ratio,
                ),
            )
            _spot_risk_requests.labels(status="ok").inc()
            return SpotRiskResponse(
                instance_type=req.instance_type,
                az=req.az,
                interruption_probability=pred["interruption_probability"],
                recommended_action=pred["recommended_action"],
                confidence=pred["confidence"],
            )
        except Exception as exc:  # noqa: BLE001
            logger.exception("Spot risk error: %s", exc)
            _spot_risk_requests.labels(status="error").inc()
            return JSONResponse(
                status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
                content={"detail": str(exc)},
            )
        finally:
            _spot_risk_latency.observe(time.perf_counter() - t0)

    @app.get("/v1/health", response_model=HealthResponse)
    async def health() -> HealthResponse:
        return HealthResponse(
            status="ok" if _state.ready else "degraded",
            models_loaded=_state.ready,
            forecaster_ready=_state.forecaster is not None,
            spot_predictor_ready=_state.spot_predictor is not None,
        )

    @app.get("/metrics")
    async def metrics() -> Response:
        data = generate_latest()
        return Response(content=data, media_type=CONTENT_TYPE_LATEST)


# ---------------------------------------------------------------------------
# Internal async helpers
# ---------------------------------------------------------------------------


async def _run_forecast(req: DemandForecastRequest) -> DemandForecastResponse:
    """Offload CPU-bound forecasting to a thread pool."""
    loop = asyncio.get_event_loop()

    def _compute() -> DemandForecastResponse:
        history_df = pd.DataFrame([p.model_dump() for p in req.history])
        result_df, model_used = _state.forecaster.forecast(  # type: ignore[union-attr]
            history_df, req.horizons_minutes
        )

        # Build ForecastPoint list
        import math as _math

        def _safe(v: Any) -> float:
            """Return 0.0 for NaN/Inf — JSON doesn't support them."""
            f = float(v)
            return 0.0 if (_math.isnan(f) or _math.isinf(f)) else f

        forecast_points: list[ForecastPoint] = []
        for _, row in result_df.iterrows():
            intervals: list[PredictionInterval] = []
            for level in (80, 95):
                lo_key = f"lo_{level}"
                hi_key = f"hi_{level}"
                if lo_key in result_df.columns and hi_key in result_df.columns:
                    lo_val = _safe(row[lo_key])
                    hi_val = _safe(row[hi_key])
                    # Only include intervals with valid bounds
                    if lo_val != 0.0 or hi_val != 0.0:
                        intervals.append(
                            PredictionInterval(level=level, lower=lo_val, upper=hi_val)
                        )
            forecast_points.append(
                ForecastPoint(
                    ds=str(row["ds"]),
                    yhat=_safe(row["yhat"]),
                    intervals=intervals,
                )
            )

        # change_percent vs tail of history at 15-min bucket
        tail_y = float(history_df["y"].iloc[-1]) if len(history_df) > 0 else 0.0
        first_yhat = forecast_points[0].yhat if forecast_points else 0.0
        change_pct = compute_change_percent(tail_y, first_yhat)
        confidence = compute_confidence(model_used, len(history_df))

        return DemandForecastResponse(
            service=req.service,
            metric=req.metric,
            forecasts=forecast_points,
            model_used=model_used,
            change_percent=change_pct,
            confidence=confidence,
        )

    return await loop.run_in_executor(None, _compute)


# ---------------------------------------------------------------------------
# State accessors (used by tests to inject pre-trained models)
# ---------------------------------------------------------------------------


def set_forecaster(f: DemandForecaster) -> None:
    _state.forecaster = f


def set_spot_predictor(sp: SpotPredictor) -> None:
    _state.spot_predictor = sp


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

app = create_app()

if __name__ == "__main__":
    import uvicorn

    uvicorn.run("app.main:app", host="0.0.0.0", port=8080, reload=False)
