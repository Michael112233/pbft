import logging
from pathlib import Path
import tempfile
import unittest
from unittest import mock

from learningagent import launcher


class LauncherLoggingTest(unittest.TestCase):
    def test_configure_logging_uses_requested_file(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            log_file = Path(temporary_directory) / "nested" / "agent.log"

            with mock.patch(
                "learningagent.launcher.logging.basicConfig"
            ) as basic_config:
                launcher._configure_logging(log_file)

        basic_config.assert_called_once_with(
            level=logging.INFO,
            format=launcher.LOG_FORMAT,
            filename=str(log_file),
            filemode="a",
            force=True,
        )

    def test_server_process_configures_node_log_before_starting(self):
        log_dir = Path("/tmp/learning-agent-test-logs")
        stop_event = mock.Mock()
        calls = []

        with (
            mock.patch(
                "learningagent.launcher._configure_logging",
                side_effect=lambda path: calls.append(("configure", path)),
            ),
            mock.patch(
                "learningagent.launcher.run_server",
                side_effect=lambda *args: calls.append(("run", args)),
            ),
        ):
            launcher._run_server_process(
                2, "local", stop_event, log_dir, "config/run2new.json"
            )

        self.assertEqual(
            calls,
            [
                ("configure", log_dir / "learning-agent-node-2.log"),
                ("run", (2, "local", stop_event, "config/run2new.json", None)),
            ],
        )

    def test_parse_args_accepts_log_directory(self):
        with mock.patch(
            "sys.argv",
            [
                "learningagent.launcher",
                "--node-count",
                "4",
                "--mode",
                "local",
                "--log-dir",
                "logs",
            ],
        ):
            args = launcher._parse_args()

        self.assertEqual(args.node_count, 4)
        self.assertEqual(args.mode, "local")
        self.assertEqual(args.log_dir, Path("logs"))
        self.assertEqual(args.node_ids, [1, 2, 3, 4])

    def test_parse_args_remote_starts_only_the_given_node(self):
        argv = ["learningagent.launcher", "--node-count", "7", "--mode", "remote"]
        with mock.patch("sys.argv", argv + ["--hosts", "h.json", "--node-ids", "3"]):
            args = launcher._parse_args()
        self.assertEqual(args.node_ids, [3])
        self.assertEqual(args.hosts, "h.json")

        for bad in (argv, argv + ["--hosts", "h.json", "--node-ids", "8"]):
            with mock.patch("sys.argv", bad), mock.patch("sys.stderr"):
                with self.assertRaises(SystemExit):
                    launcher._parse_args()


if __name__ == "__main__":
    unittest.main()
