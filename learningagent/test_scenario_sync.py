"""The Python schedule must agree with node/scenario.go exactly."""

import json
import tempfile
import unittest
from pathlib import Path

from learningagent.scenario_sync import (
    DEFAULT_SCENARIO_GENERATIONS,
    Scenario,
    load_scenario_schedule,
    scenario_for_sequence,
)

ROTATION = [Scenario.Healthy, Scenario.ProposalDelay, Scenario.NetworkDelay]

BASE_CONFIG = {
    "scenario_mode": True,
    "scenarios": ["Healthy", "ProposalDelay", "NetworkDelay"],
    "scenario_generations": 100,
}


def write_config(directory: str, **overrides) -> str:
    config = {**BASE_CONFIG, **overrides}
    path = Path(directory) / "config.json"
    path.write_text(json.dumps(config))
    return str(path)


class ScenarioForSequenceTest(unittest.TestCase):
    def test_matches_go_table(self):
        # Same cases as TestScenarioForGeneration in node/scenario_test.go.
        cases = [
            (1, Scenario.Healthy),
            (100, Scenario.Healthy),
            (101, Scenario.ProposalDelay),
            (160, Scenario.ProposalDelay),
            (200, Scenario.ProposalDelay),
            (201, Scenario.NetworkDelay),
            (300, Scenario.NetworkDelay),
            (301, Scenario.Healthy),
            (601, Scenario.Healthy),
        ]
        for sequence_id, expected in cases:
            with self.subTest(sequence_id=sequence_id):
                self.assertEqual(
                    scenario_for_sequence(sequence_id, ROTATION, 100), expected
                )

    def test_span_of_one_advances_every_sequence(self):
        got = [scenario_for_sequence(seq, ROTATION, 1) for seq in range(1, 5)]
        self.assertEqual(
            got,
            [
                Scenario.Healthy,
                Scenario.ProposalDelay,
                Scenario.NetworkDelay,
                Scenario.Healthy,
            ],
        )

    def test_rejects_sequence_below_one(self):
        with self.assertRaises(ValueError):
            scenario_for_sequence(0, ROTATION, 100)


class LoadScenarioScheduleTest(unittest.TestCase):
    def test_reads_go_names_and_span(self):
        with tempfile.TemporaryDirectory() as directory:
            scenarios, span = load_scenario_schedule(write_config(directory))
        self.assertEqual(scenarios, ROTATION)
        self.assertEqual(span, 100)

    def test_span_defaults_when_absent(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.json"
            config = dict(BASE_CONFIG)
            del config["scenario_generations"]
            path.write_text(json.dumps(config))
            _, span = load_scenario_schedule(str(path))
        self.assertEqual(span, DEFAULT_SCENARIO_GENERATIONS)

    def test_scenario_mode_off_holds_first_scenario(self):
        with tempfile.TemporaryDirectory() as directory:
            scenarios, span = load_scenario_schedule(
                write_config(directory, scenario_mode=False)
            )
        self.assertEqual(scenarios, [Scenario.Healthy])
        for sequence_id in (1, 101, 5000):
            self.assertEqual(
                scenario_for_sequence(sequence_id, scenarios, span), Scenario.Healthy
            )

    def test_rejects_unknown_scenario_name(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_config(directory, scenarios=["Healthy", "Meltdown"])
            with self.assertRaises(RuntimeError) as context:
                load_scenario_schedule(path)
        self.assertIn("Meltdown", str(context.exception))

    def test_rejects_missing_file(self):
        with self.assertRaises(RuntimeError):
            load_scenario_schedule("/nonexistent/config.json")

    def test_rejects_non_positive_span(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_config(directory, scenario_generations=-1)
            with self.assertRaises(RuntimeError):
                load_scenario_schedule(path)

    def test_rejects_empty_scenarios_when_enabled(self):
        with tempfile.TemporaryDirectory() as directory:
            path = write_config(directory, scenarios=[])
            with self.assertRaises(RuntimeError):
                load_scenario_schedule(path)

    def test_reads_the_real_experiment_config(self):
        scenarios, span = load_scenario_schedule("config/run2new.json")
        self.assertTrue(scenarios)
        self.assertGreater(span, 0)


if __name__ == "__main__":
    unittest.main()
