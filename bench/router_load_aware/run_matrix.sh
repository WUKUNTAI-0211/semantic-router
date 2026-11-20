#!/usr/bin/env bash
# Every policy under both loads, ROUNDS times, then the summary table.
HERE=$(cd "$(dirname "$0")" && pwd)
ROUNDS=${ROUNDS:-2} DURATION=${DURATION:-1200}
for r in $(seq 1 "$ROUNDS"); do
  for arm in "least_request uniform" "load_aware uniform" "least_request uneven" "load_aware uneven"; do
    echo "== r$r $arm $(date +%T)"
    # shellcheck disable=SC2086
    TAG=r$r "$HERE/run_arm.sh" $arm "$DURATION"
  done
done
docker rm -f lb-bench-vllm-1 lb-bench-vllm-2 >/dev/null 2>&1
"${PYTHON:-${WORK:-$HERE/work}/venv/bin/python}" "$HERE/report.py" "${RESULTS:-$HERE/results}"/r*-*
