#!/usr/bin/env bash
# One arm: two fresh vLLM replicas, the Router with POLICY, optional bypass load on replica 1, then the tester.
# Usage: run_arm.sh POLICY(least_request|load_aware) LOAD(uniform|uneven) [DURATION]
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
POLICY=$1 LOAD=$2 DURATION=${3:-1200}
WORK=${WORK:-$HERE/work}
ROUTER=${ROUTER:-$WORK/router}
TESTER_DIR=${TESTER_DIR:-$WORK/kv-cache-tester}
TRACES=${TRACES:-$WORK/traces}
PYTHON=${PYTHON:-$WORK/venv/bin/python}
MODEL=${MODEL:-Qwen/Qwen3-0.6B}
VLLM_IMAGE=${VLLM_IMAGE:-vllm/vllm-openai:v0.26.0}
# One GB10 shares its memory between replicas, so each gets a fixed 24 GiB KV pool. On separate GPUs set
# GPU_1=device=0 GPU_2=device=1 and VLLM_MEM_ARGS="--gpu-memory-utilization 0.9".
GPU_1=${GPU_1:-all} GPU_2=${GPU_2:-all}
VLLM_MEM_ARGS=${VLLM_MEM_ARGS:---gpu-memory-utilization 0.12 --kv-cache-memory-bytes 25769803776}
MAX_NUM_SEQS=${MAX_NUM_SEQS:-4} MAX_CONTEXT=${MAX_CONTEXT:-32000}
BYPASS_CONC=${BYPASS_CONC:-8} BYPASS_OUT=${BYPASS_OUT:-1024}
OUT=${RESULTS:-$HERE/results}/${TAG:+$TAG-}$POLICY-$LOAD
rm -rf "$OUT"; mkdir -p "$OUT"
# YaRN to 128k; transformers 5 drops rope_theta unless it is given in rope_parameters.
ROPE='{"rope_parameters":{"rope_type":"yarn","factor":4.0,"original_max_position_embeddings":32768,"rope_theta":1000000},"max_position_embeddings":131072}'

cleanup() {
  [ -n "${SAMPLER:-}" ] && kill "$SAMPLER" 2>/dev/null
  [ -n "${BYPASS:-}" ] && kill "$BYPASS" 2>/dev/null
  [ -n "${ROUTER_PID:-}" ] && kill "$ROUTER_PID" 2>/dev/null
  wait 2>/dev/null
}
trap cleanup EXIT

docker rm -f lb-bench-vllm-1 lb-bench-vllm-2 >/dev/null 2>&1
for i in 1 2; do
  gpu_var=GPU_$i
  # shellcheck disable=SC2086
  docker run -d --name lb-bench-vllm-$i --gpus "${!gpu_var}" --ipc=host -p 1875$i:8000 \
    -v "$HOME/.cache/huggingface:/root/.cache/huggingface" "$VLLM_IMAGE" \
    --model "$MODEL" $VLLM_MEM_ARGS --max-model-len 131072 --enable-prefix-caching \
    --hf-overrides "$ROPE" --max-num-seqs "$MAX_NUM_SEQS" >/dev/null
  ok=0
  for _ in $(seq 1 60); do curl -sf -m 2 http://127.0.0.1:1875$i/health >/dev/null && { ok=1; break; }; sleep 5; done
  [ $ok = 1 ] || { echo "replica $i failed"; docker logs lb-bench-vllm-$i >"$OUT/replica$i.log" 2>&1; exit 1; }
done

"$ROUTER" -config "$HERE/config-$POLICY.yaml" -gateway standalone -api-port 18081 -metrics-port 19191 \
  -port 50161 >"$OUT/router.log" 2>&1 &
ROUTER_PID=$!
for _ in $(seq 1 30); do curl -sf -m 2 http://127.0.0.1:18899/v1/models >/dev/null && break; sleep 1; done

# Every 5 s: each replica's waiting/running and the Router's load_aware pick outcomes.
( while :; do
    t=$(date +%s)
    for i in 1 2; do
      curl -s -m 2 http://127.0.0.1:1875$i/metrics | awk -v t="$t" -v r="$i" \
        '/^vllm:num_requests_(waiting|running)\{/ {split($1,a,"{"); print t, "replica"r, a[1], $2}'
    done
    curl -s -m 2 http://127.0.0.1:19191/metrics | awk -v t="$t" '/^llm_upstream_load_aware_picks_total/ {print t, "router", $1, $2}'
    sleep 5
  done >"$OUT/samples.txt" ) &
SAMPLER=$!

if [ "$LOAD" = uneven ]; then
  "$PYTHON" "$HERE/bypass.py" http://127.0.0.1:18751 "$BYPASS_CONC" 3000 "$BYPASS_OUT" "$MODEL" >"$OUT/bypass.log" 2>&1 &
  BYPASS=$!
  sleep 20
fi

"$PYTHON" "$TESTER_DIR/trace_replay_tester.py" --api-endpoint http://127.0.0.1:18899 \
  --trace-directory "$TRACES" --tokenizer "$MODEL" --start-users 4 --max-users 16 --max-ttft 60 \
  --test-duration "$DURATION" --max-context "$MAX_CONTEXT" --warm-prefix-pct 0.5 --timing-strategy think-only \
  --recycle --seed 0 --trace-seed 0 --prompt-seed 0 --skip-graphs --no-color --output-dir "$OUT" >"$OUT/client.log" 2>&1
echo "tester exit=$?"

for i in 1 2; do curl -s -m 5 http://127.0.0.1:1875$i/metrics >"$OUT/replica$i.metrics"; done
curl -s -m 5 http://127.0.0.1:19191/metrics >"$OUT/router.metrics"
echo "arm done $OUT"
