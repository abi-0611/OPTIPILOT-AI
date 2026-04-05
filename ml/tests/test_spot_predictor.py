"""
Tests for ml/app/spot_predictor.py — synthetic training + prediction validation.
"""
import json
import tempfile
from pathlib import Path

import pytest

from app.spot_predictor import SpotPredictor, _MIGRATE_THRESHOLD, _SWITCH_TO_OD_THRESHOLD


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture(scope="module")
def trained_predictor() -> SpotPredictor:
    """Module-scoped predictor trained once on synthetic data."""
    sp = SpotPredictor()
    sp.train(n_synthetic=3000)
    return sp


# ---------------------------------------------------------------------------
# Training
# ---------------------------------------------------------------------------


class TestSpotPredictorTraining:

    def test_train_creates_model(self):
        sp = SpotPredictor()
        assert not sp.ready
        sp.train(n_synthetic=500)
        assert sp.ready

    def test_synthetic_data_has_expected_columns(self):
        sp = SpotPredictor()
        data = sp._generate_synthetic(100)
        expected = {
            "instance_type", "az", "hour_of_day", "day_of_week",
            "recent_interruption_count_7d", "spot_price_ratio", "interrupted",
        }
        assert expected.issubset(set(data.columns))

    def test_synthetic_data_has_both_classes(self):
        sp = SpotPredictor()
        data = sp._generate_synthetic(1000)
        assert set(data["interrupted"].unique()) == {0, 1}


# ---------------------------------------------------------------------------
# Prediction
# ---------------------------------------------------------------------------


class TestSpotPredictorPrediction:

    def test_predict_returns_required_keys(self, trained_predictor: SpotPredictor):
        result = trained_predictor.predict(
            instance_type="m5.xlarge",
            az="us-east-1a",
            hour_of_day=14,
            day_of_week=2,
        )
        assert "interruption_probability" in result
        assert "recommended_action" in result
        assert "confidence" in result

    def test_probability_bounded_0_1(self, trained_predictor: SpotPredictor):
        result = trained_predictor.predict(
            instance_type="c5.large",
            az="us-west-2a",
            hour_of_day=3,
            day_of_week=6,
        )
        assert 0.0 <= result["interruption_probability"] <= 1.0

    def test_confidence_bounded_0_1(self, trained_predictor: SpotPredictor):
        result = trained_predictor.predict(
            instance_type="m5.large",
            az="us-east-1b",
            hour_of_day=10,
            day_of_week=1,
        )
        assert 0.0 <= result["confidence"] <= 1.0

    def test_action_is_valid_literal(self, trained_predictor: SpotPredictor):
        result = trained_predictor.predict(
            instance_type="r5.xlarge",
            az="us-east-1a",
            hour_of_day=12,
            day_of_week=3,
        )
        assert result["recommended_action"] in {"keep", "migrate", "switch_to_od"}

    def test_high_risk_scenario(self, trained_predictor: SpotPredictor):
        """r5 in us-east-1a during business hours with many recent interruptions
        should produce a higher probability than a low-risk scenario."""
        high = trained_predictor.predict(
            instance_type="r5.2xlarge",
            az="us-east-1a",
            hour_of_day=14,
            day_of_week=2,
            recent_interruption_count_7d=15,
            spot_price_ratio=1.3,
        )
        low = trained_predictor.predict(
            instance_type="m5.large",
            az="us-west-2c",
            hour_of_day=3,
            day_of_week=6,
            recent_interruption_count_7d=0,
            spot_price_ratio=0.3,
        )
        assert high["interruption_probability"] > low["interruption_probability"]

    def test_unknown_instance_type_no_crash(self, trained_predictor: SpotPredictor):
        """An instance type not in the training set should still return a result
        (all one-hot bits are 0, which is valid)."""
        result = trained_predictor.predict(
            instance_type="p4d.24xlarge",
            az="us-east-1a",
            hour_of_day=10,
            day_of_week=1,
        )
        assert 0.0 <= result["interruption_probability"] <= 1.0

    def test_unknown_az_no_crash(self, trained_predictor: SpotPredictor):
        result = trained_predictor.predict(
            instance_type="m5.xlarge",
            az="eu-west-1a",
            hour_of_day=10,
            day_of_week=1,
        )
        assert 0.0 <= result["interruption_probability"] <= 1.0

    def test_predict_before_train_raises(self):
        sp = SpotPredictor()
        with pytest.raises(RuntimeError, match="not trained"):
            sp.predict(
                instance_type="m5.large",
                az="us-east-1a",
                hour_of_day=10,
                day_of_week=1,
            )


# ---------------------------------------------------------------------------
# Recommended action thresholds
# ---------------------------------------------------------------------------


class TestRecommendedAction:

    def test_keep_for_low_prob(self):
        assert SpotPredictor._recommend(0.1) == "keep"

    def test_migrate_for_medium_prob(self):
        assert SpotPredictor._recommend(0.4) == "migrate"

    def test_switch_for_high_prob(self):
        assert SpotPredictor._recommend(0.7) == "switch_to_od"

    def test_boundary_migrate(self):
        assert SpotPredictor._recommend(_MIGRATE_THRESHOLD) == "migrate"

    def test_boundary_switch(self):
        assert SpotPredictor._recommend(_SWITCH_TO_OD_THRESHOLD) == "switch_to_od"


# ---------------------------------------------------------------------------
# Persistence (save / load)
# ---------------------------------------------------------------------------


class TestSpotPredictorPersistence:

    def test_save_and_load_roundtrip(self, trained_predictor: SpotPredictor):
        with tempfile.TemporaryDirectory() as tmpdir:
            model_path = Path(tmpdir) / "spot_model.json"
            trained_predictor.save(model_path)

            assert model_path.exists()
            assert model_path.with_suffix(".meta.json").exists()

            loaded = SpotPredictor()
            loaded.load(model_path)
            assert loaded.ready

            # Predictions should match
            orig = trained_predictor.predict(
                instance_type="m5.xlarge", az="us-east-1a",
                hour_of_day=10, day_of_week=1,
            )
            loaded_pred = loaded.predict(
                instance_type="m5.xlarge", az="us-east-1a",
                hour_of_day=10, day_of_week=1,
            )
            assert abs(orig["interruption_probability"] - loaded_pred["interruption_probability"]) < 1e-6

    def test_load_nonexistent_raises(self):
        sp = SpotPredictor()
        with pytest.raises(FileNotFoundError):
            sp.load("/nonexistent/path/model.json")

    def test_save_without_train_raises(self):
        sp = SpotPredictor()
        with pytest.raises(RuntimeError, match="No model"):
            sp.save("/tmp/nope.json")

    def test_meta_preserves_encoding(self, trained_predictor: SpotPredictor):
        with tempfile.TemporaryDirectory() as tmpdir:
            model_path = Path(tmpdir) / "spot_model.json"
            trained_predictor.save(model_path)

            meta = json.loads(model_path.with_suffix(".meta.json").read_text(encoding="utf-8"))
            assert meta["instance_types"] == trained_predictor.instance_types
            assert meta["azs"] == trained_predictor.azs


# ---------------------------------------------------------------------------
# Confidence heuristic
# ---------------------------------------------------------------------------


class TestConfidence:

    def test_confident_at_extremes(self):
        assert SpotPredictor._confidence(0.0) == 1.0
        assert SpotPredictor._confidence(1.0) == 1.0

    def test_least_confident_at_half(self):
        assert SpotPredictor._confidence(0.5) == 0.5

    def test_monotonic_from_half(self):
        c1 = SpotPredictor._confidence(0.5)
        c2 = SpotPredictor._confidence(0.7)
        c3 = SpotPredictor._confidence(0.9)
        assert c1 <= c2 <= c3
