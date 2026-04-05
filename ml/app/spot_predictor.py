"""
Spot interruption predictor using XGBoost.

For MVP the model trains on synthetic data that approximates real AWS spot
interruption patterns.  A pre-trained model can be saved to / loaded from
JSON for fast startup.

Features (per prediction):
  instance_type  – categorical → one-hot encoded
  az             – categorical → one-hot encoded
  hour_of_day    – int 0-23
  day_of_week    – int 0-6 (Mon-Sun)
  recent_interruption_count_7d – int ≥ 0
  spot_price_ratio             – float > 0  (spot / on-demand)

Output:
  interruption_probability  – float [0, 1]
  recommended_action        – "keep" | "migrate" | "switch_to_od"
  confidence                – float [0, 1]
"""
from __future__ import annotations

import json
import logging
import os
from pathlib import Path

import numpy as np
import pandas as pd
import xgboost as xgb

logger = logging.getLogger(__name__)

# Thresholds for recommended actions
_MIGRATE_THRESHOLD = 0.3
_SWITCH_TO_OD_THRESHOLD = 0.6

# Known instance types and AZs for one-hot encoding (MVP set)
DEFAULT_INSTANCE_TYPES = [
    "m5.large", "m5.xlarge", "m5.2xlarge",
    "c5.large", "c5.xlarge", "c5.2xlarge",
    "r5.large", "r5.xlarge", "r5.2xlarge",
]
DEFAULT_AZS = [
    "us-east-1a", "us-east-1b", "us-east-1c",
    "us-west-2a", "us-west-2b", "us-west-2c",
]


class SpotPredictor:
    """XGBoost binary classifier for spot interruption probability."""

    def __init__(
        self,
        instance_types: list[str] | None = None,
        azs: list[str] | None = None,
    ) -> None:
        self.instance_types = instance_types or DEFAULT_INSTANCE_TYPES
        self.azs = azs or DEFAULT_AZS
        self._model: xgb.XGBClassifier | None = None

    @property
    def ready(self) -> bool:
        return self._model is not None

    # ── feature engineering ───────────────────────────────────────────────

    def _encode(self, rows: list[dict]) -> pd.DataFrame:
        """Build a feature DataFrame from raw dicts."""
        df = pd.DataFrame(rows)

        # One-hot instance type
        for it in self.instance_types:
            col = f"it_{it}"
            df[col] = (df["instance_type"] == it).astype(int)

        # One-hot AZ
        for az in self.azs:
            col = f"az_{az}"
            df[col] = (df["az"] == az).astype(int)

        feature_cols = (
            [f"it_{it}" for it in self.instance_types]
            + [f"az_{az}" for az in self.azs]
            + ["hour_of_day", "day_of_week", "recent_interruption_count_7d", "spot_price_ratio"]
        )
        return df[feature_cols].astype(float)

    # ── training ──────────────────────────────────────────────────────────

    def train(self, data: pd.DataFrame | None = None, n_synthetic: int = 5000) -> None:
        """Train the model.  If *data* is None, generate synthetic data."""
        if data is None:
            data = self._generate_synthetic(n_synthetic)

        X = self._encode(data.to_dict("records"))
        y = data["interrupted"].astype(int)

        self._model = xgb.XGBClassifier(
            n_estimators=100,
            max_depth=4,
            learning_rate=0.1,
            eval_metric="logloss",
            random_state=42,
        )
        self._model.fit(X, y)
        logger.info("SpotPredictor trained on %d samples", len(data))

    # ── prediction ────────────────────────────────────────────────────────

    def predict(
        self,
        instance_type: str,
        az: str,
        hour_of_day: int,
        day_of_week: int,
        recent_interruption_count_7d: int = 0,
        spot_price_ratio: float = 1.0,
    ) -> dict:
        """Return interruption probability, recommended action, and confidence.

        Returns:
            dict with keys ``interruption_probability``, ``recommended_action``,
            ``confidence``.
        """
        if self._model is None:
            raise RuntimeError("SpotPredictor is not trained — call train() first")

        row = {
            "instance_type": instance_type,
            "az": az,
            "hour_of_day": hour_of_day,
            "day_of_week": day_of_week,
            "recent_interruption_count_7d": recent_interruption_count_7d,
            "spot_price_ratio": spot_price_ratio,
        }
        X = self._encode([row])
        prob = float(self._model.predict_proba(X)[0, 1])

        action = self._recommend(prob)
        confidence = self._confidence(prob)

        return {
            "interruption_probability": prob,
            "recommended_action": action,
            "confidence": confidence,
        }

    @staticmethod
    def _recommend(prob: float) -> str:
        if prob >= _SWITCH_TO_OD_THRESHOLD:
            return "switch_to_od"
        if prob >= _MIGRATE_THRESHOLD:
            return "migrate"
        return "keep"

    @staticmethod
    def _confidence(prob: float) -> float:
        """Higher confidence when the model is more decisive (prob near 0 or 1)."""
        return round(min(1.0, 0.5 + abs(prob - 0.5)), 4)

    # ── persistence ───────────────────────────────────────────────────────

    def save(self, path: str | Path) -> None:
        """Save trained model to JSON."""
        if self._model is None:
            raise RuntimeError("No model to save")
        path = Path(path)
        path.parent.mkdir(parents=True, exist_ok=True)
        self._model.save_model(str(path))
        # Also save encoding metadata
        meta_path = path.with_suffix(".meta.json")
        meta_path.write_text(
            json.dumps({"instance_types": self.instance_types, "azs": self.azs}),
            encoding="utf-8",
        )
        logger.info("SpotPredictor saved to %s", path)

    def load(self, path: str | Path) -> None:
        """Load model from JSON."""
        path = Path(path)
        if not path.exists():
            raise FileNotFoundError(f"Model file not found: {path}")
        self._model = xgb.XGBClassifier()
        self._model.load_model(str(path))
        # Load encoding metadata if available
        meta_path = path.with_suffix(".meta.json")
        if meta_path.exists():
            meta = json.loads(meta_path.read_text(encoding="utf-8"))
            self.instance_types = meta.get("instance_types", self.instance_types)
            self.azs = meta.get("azs", self.azs)
        logger.info("SpotPredictor loaded from %s", path)

    # ── synthetic data ────────────────────────────────────────────────────

    def _generate_synthetic(self, n: int) -> pd.DataFrame:
        """Generate synthetic spot interruption data mimicking AWS patterns.

        Rules baked in (so the model can learn):
        - Higher interruption rate during business hours (9-17 UTC weekdays)
        - r5 instances have higher interruption rates than m5/c5
        - Some AZs are "hotspots" (us-east-1a)
        - Higher spot_price_ratio → higher interruption
        - More recent interruptions → more likely to see another
        """
        rng = np.random.default_rng(42)

        rows: list[dict] = []
        for _ in range(n):
            it = rng.choice(self.instance_types)
            az = rng.choice(self.azs)
            hour = int(rng.integers(0, 24))
            dow = int(rng.integers(0, 7))
            recent = int(rng.integers(0, 20))
            price_ratio = float(rng.uniform(0.2, 1.5))

            # Base probability
            p = 0.05

            # Business hours bump
            if 9 <= hour <= 17 and dow < 5:
                p += 0.10

            # Instance family bump
            if it.startswith("r5"):
                p += 0.12
            elif it.startswith("c5"):
                p += 0.05

            # AZ hotspot
            if az == "us-east-1a":
                p += 0.08

            # Price pressure
            if price_ratio > 0.8:
                p += 0.10 * (price_ratio - 0.8)

            # Recent-interruption momentum
            p += 0.01 * min(recent, 10)

            # Clamp and add noise
            p = np.clip(p + rng.normal(0, 0.03), 0.01, 0.99)
            interrupted = int(rng.random() < p)

            rows.append({
                "instance_type": it,
                "az": az,
                "hour_of_day": hour,
                "day_of_week": dow,
                "recent_interruption_count_7d": recent,
                "spot_price_ratio": price_ratio,
                "interrupted": interrupted,
            })

        return pd.DataFrame(rows)
