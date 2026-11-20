#!/usr/bin/env bash
# Prepare a run under WORK: the Router binary, WEKA's kv-cache-tester at the pinned commit, a Python venv,
# and the SemiAnalysis traces (split by bench/agent_kv_tier/prepare_traces.py from main).
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=${WORK:-$HERE/work}
TESTER_REF=${TESTER_REF:-94f6046dfd9c0c8bc0cdb24a5aa6579bac8669bf}
mkdir -p "$WORK"
cd "$WORK"

[ -x router ] || (cd "$HERE/../../src/semantic-router" && go build -o "$WORK/router" ./cmd/)
[ -d kv-cache-tester ] || git clone https://github.com/callanjfox/kv-cache-tester.git
git -C kv-cache-tester checkout -q "$TESTER_REF"
[ -d venv ] || python3 -m venv venv
venv/bin/pip install -q openai transformers pandas numpy requests plotly huggingface_hub aiohttp tabulate
if [ ! -d traces ] || [ -z "$(ls traces)" ]; then
  echo "put the 336 traces in $WORK/traces, e.g. python bench/agent_kv_tier/prepare_traces.py --out $WORK/traces from main"
  exit 1
fi
echo "setup done in $WORK"
