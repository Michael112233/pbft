"""Compatibility entry point for running a single learning-agent server."""

from pathlib import Path
import sys

if __package__ in (None, ""):
    sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from learningagent.server import main


if __name__ == "__main__":
    raise SystemExit(main())
