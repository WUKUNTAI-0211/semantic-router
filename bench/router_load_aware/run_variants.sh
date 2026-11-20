#!/usr/bin/env bash
# Experiment only, with this commit's Router patch: load_aware scored three ways, interleaved, uniform and
# uneven load. A = engine waiting + the Router's in-flight requests (as merged), B = engine waiting + running,
# C = B + the Router's requests started after the last scrape. SR_LOAD_AWARE_SCORE selects B or C.
HERE=$(cd "$(dirname "$0")" && pwd)
ROUNDS=${ROUNDS:-2} DURATION=${DURATION:-1200}
for r in $(seq 1 "$ROUNDS"); do
  for load in uniform uneven; do
    for v in "A " "B engine" "C engine_plus_new"; do
      # shellcheck disable=SC2086
      set -- $v
      echo "== r$r $load $1 $(date +%T)"
      TAG=var$1-r$r SR_LOAD_AWARE_SCORE=${2:-} "$HERE/run_arm.sh" load_aware "$load" "$DURATION"
    done
  done
done
docker rm -f lb-bench-vllm-1 lb-bench-vllm-2 >/dev/null 2>&1
