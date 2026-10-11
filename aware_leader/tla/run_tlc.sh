#!/usr/bin/env bash
# Model-check AwareSchedule.tla with one or more configs.
#   ./run_tlc.sh n4_certified.cfg [more.cfg ...]
# Needs Java 11+ (JAVA or java on PATH) and tla2tools.jar (TLA2TOOLS, or it is
# downloaded once to ~/.cache/tla2tools.jar).
set -euo pipefail
cd "$(dirname "$0")"
JAVA=${JAVA:-java}
JAR=${TLA2TOOLS:-$HOME/.cache/tla2tools.jar}
if [ ! -f "$JAR" ]; then
  mkdir -p "$(dirname "$JAR")"
  curl -sSfL -o "$JAR" https://github.com/tlaplus/tlaplus/releases/download/v1.8.0/tla2tools.jar
fi
mkdir -p out
for cfg in "$@"; do
  name=$(basename "$cfg" .cfg)
  echo "== $name"
  "$JAVA" -XX:+UseParallelGC -cp "$JAR" tlc2.TLC -workers auto -deadlock -noGenerateSpecTE \
    -metadir "out/states_$name" -config "$cfg" AwareSchedule.tla > "out/$name.txt" 2>&1 || true
  grep -E "is violated|were violated|No error has been found|^[0-9]+ states generated" "out/$name.txt"
done
