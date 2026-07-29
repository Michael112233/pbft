import unittest

from learningagent.server import (
    EpsilonGreedyBandit,
    MultiRF,
    ProtocolName,
)


class EpsilonGreedyBanditTest(unittest.TestCase):
    def test_multirf_remains_available(self):
        self.assertIsNotNone(MultiRF)

    def test_first_two_predictions_cover_both_protocols(self):
        bandit = EpsilonGreedyBandit(seed=5)

        selected = {bandit.predict(), bandit.predict()}

        self.assertEqual(selected, set(ProtocolName))
        self.assertEqual(
            bandit.selection_counts,
            {
                ProtocolName.Periodic: 1,
                ProtocolName.Performance: 1,
            },
        )

    def test_train_updates_only_selected_protocol(self):
        bandit = EpsilonGreedyBandit(alpha=0.25)

        bandit.train(ProtocolName.Periodic, 800.0)

        self.assertEqual(bandit.values[ProtocolName.Periodic], 200.0)
        self.assertEqual(bandit.values[ProtocolName.Performance], 0.0)
        self.assertEqual(bandit.reward_counts[ProtocolName.Periodic], 1)
        self.assertEqual(bandit.reward_counts[ProtocolName.Performance], 0)

    def test_train_uses_constant_step_update(self):
        bandit = EpsilonGreedyBandit(alpha=0.25)

        bandit.train(ProtocolName.Periodic, 800.0)
        bandit.train(ProtocolName.Periodic, 1000.0)

        self.assertEqual(bandit.values[ProtocolName.Periodic], 400.0)
        self.assertEqual(bandit.reward_counts[ProtocolName.Periodic], 2)

    def test_exploitation_selects_protocol_with_higher_value(self):
        bandit = EpsilonGreedyBandit(epsilon=0.0, alpha=1.0, seed=5)
        bandit.predict()
        bandit.predict()
        bandit.train(ProtocolName.Periodic, 500.0)
        bandit.train(ProtocolName.Performance, 900.0)

        selections = [bandit.predict() for _ in range(20)]

        self.assertEqual(selections, [ProtocolName.Performance] * 20)

    def test_seeded_exploration_is_reproducible(self):
        first = EpsilonGreedyBandit(epsilon=1.0, seed=7)
        second = EpsilonGreedyBandit(epsilon=1.0, seed=7)

        first_selections = [first.predict() for _ in range(20)]
        second_selections = [second.predict() for _ in range(20)]

        self.assertEqual(first_selections, second_selections)
        self.assertEqual(set(first_selections), set(ProtocolName))

    def test_seeded_tie_breaking_is_reproducible(self):
        first = EpsilonGreedyBandit(epsilon=0.0, seed=11)
        second = EpsilonGreedyBandit(epsilon=0.0, seed=11)
        first.predict()
        first.predict()
        second.predict()
        second.predict()

        first_selections = [first.predict() for _ in range(20)]
        second_selections = [second.predict() for _ in range(20)]

        self.assertEqual(first_selections, second_selections)
        self.assertEqual(set(first_selections), set(ProtocolName))

    def test_train_accepts_protocol_string(self):
        bandit = EpsilonGreedyBandit(alpha=1.0)

        bandit.train("performance", 1234.5)

        self.assertEqual(
            bandit.values[ProtocolName.Performance],
            1234.5,
        )

    def test_rejects_invalid_constructor_parameters(self):
        for epsilon in (-0.01, 1.01, float("nan"), float("inf")):
            with self.subTest(epsilon=epsilon):
                with self.assertRaises(ValueError):
                    EpsilonGreedyBandit(epsilon=epsilon)

        for alpha in (0.0, -0.01, 1.01, float("nan"), float("inf")):
            with self.subTest(alpha=alpha):
                with self.assertRaises(ValueError):
                    EpsilonGreedyBandit(alpha=alpha)

    def test_rejects_unknown_protocol(self):
        bandit = EpsilonGreedyBandit()

        with self.assertRaisesRegex(ValueError, "unknown protocol"):
            bandit.train("unknown", 100.0)

    def test_rejects_non_finite_reward(self):
        bandit = EpsilonGreedyBandit()

        for reward in (float("nan"), float("inf"), float("-inf")):
            with self.subTest(reward=reward):
                with self.assertRaisesRegex(ValueError, "reward must be finite"):
                    bandit.train(ProtocolName.Periodic, reward)


if __name__ == "__main__":
    unittest.main()
