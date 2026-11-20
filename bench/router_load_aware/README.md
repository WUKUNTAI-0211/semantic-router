# Router `load_aware` vs `least_request` (#6871)

Two vLLM replicas of one model behind the Router's standalone gateway, with backend telemetry on. WEKA's
kv-cache-tester replays the SemiAnalysis agent traces from #5358 through the Router. Each arm runs one
`lb_policy` under one load:

- **uniform**: only the tester's traffic.
- **uneven**: `bypass.py` also keeps 8 requests (3,000 words in, 1,024 tokens out, never a prefix hit) in flight
  on replica 1, straight to vLLM, where the Router cannot count them.

Built on #4986's head (6f8721590), which adds `load_aware`.

## Run

```bash
bench/router_load_aware/setup.sh              # Router binary, tester, venv; traces as it says
ROUNDS=2 bench/router_load_aware/run_matrix.sh
```

`run_arm.sh POLICY LOAD [DURATION]` runs one arm. Results land in `results/<tag>-<policy>-<load>/`, and
`report.py DIR...` prints the table below (`report.py records/*` reproduces it). Environment overrides: `WORK`, `ROUTER`, `TESTER_DIR`, `TRACES`,
`PYTHON`, `MODEL`, `VLLM_IMAGE`, `GPU_1`, `GPU_2`, `VLLM_MEM_ARGS`, `MAX_NUM_SEQS`, `MAX_CONTEXT`, `BYPASS_CONC`,
`BYPASS_OUT`, `DURATION`. The two configs name `Qwen/Qwen3-0.6B`; edit them when you change `MODEL`.

**On two GPUs**, set `GPU_1=device=0 GPU_2=device=1` and `VLLM_MEM_ARGS="--gpu-memory-utilization 0.9"`. The
defaults fit both replicas on one GB10, whose memory is shared, and `MAX_NUM_SEQS=4` exists only so that
uneven load builds a queue there; on separate GPUs, revisit it and the bypass load.

## Results

One GB10, vLLM 0.26, Qwen3-0.6B with YaRN to 128k, 24 GiB of KV per replica, at most 4 running requests per
replica, 32k max context, 20 minutes per arm. Trimmed raw results are in `records/` (the tester's per-request
rows, 5 s queue samples, the Router's split and each replica's prefix-cache counters).

| Load | Policy | Run | Requests | TTFT p50 / p90 / p99 | Router to replica 1 / 2 | Mean waiting, replica 1 / 2 |
| --- | --- | --- | --- | --- | --- | --- |
| uniform | least_request | 1 | 474 | 5.3 / 40.4 / 69.3 s | 236 / 244 | 2.2 / 2.3 |
| uniform | least_request | 2 | 503 | 5.0 / 38.1 / 75.0 s | 268 / 236 | 2.2 / 2.1 |
| uniform | load_aware | 1 | 491 | 6.5 / 31.6 / 60.1 s | 205 / 288 | 2.1 / 2.0 |
| uniform | load_aware | 2 | 481 | 5.5 / 30.6 / 85.9 s | 181 / 303 | 2.2 / 1.7 |
| uneven | least_request | 1 | 389 | 5.8 / 81.1 / 108.5 s | 125 / 269 | 11.8 / 1.3 |
| uneven | least_request | 2 | 398 | 4.7 / 90.2 / 149.7 s | 117 / 281 | 11.9 / 0.9 |
| uneven | load_aware | 1 | 345 | 25.7 / 66.6 / 90.0 s | 84 / 263 | 8.5 / 4.1 |
| uneven | load_aware | 2 | 384 | 18.2 / 65.9 / 107.8 s | 76 / 316 | 8.4 / 4.0 |

Under uniform load the two policies are within noise. Under uneven load `load_aware` trades the median for the
tail: it scores a replica as engine waiting plus the Router's own in-flight requests, evens the queues out, and
more Router requests wait a little. The tester adapts its load to TTFT, so request counts follow the policy and
are not a throughput measure, and a p99 over about 400 requests rests on about 4 of them.

## Limits

- Both replicas share one GPU's compute, so moving load off one replica also slows the other. A recommendation
  needs separate GPUs.
- At a 100k max context, four running sessions overflow the KV pool, prefix hits fall to about zero and each arm
  completes only about 60 requests, too few to compare.
- vLLM's prefix-cache queries counter counts waiting requests again on every step; use hits against prompt tokens.
- The Router config needs a `recipes` default route and an `entrypoints` entry for `vllm-sr/auto`, or the tester's
  requests get 400; the tester needs `aiohttp`, or it can't read the model name.

## Experiment: other load_aware scores

Not part of #6871's scope, which leaves Router changes to their own issues. This commit's Router patch lets
`SR_LOAD_AWARE_SCORE` swap the score, and `run_variants.sh` runs the three interleaved in one overnight batch with
no other load on the GPU, so compare them only with each other (`records/var*`):

| Score | Uneven TTFT p50, run 1 / 2 | Requests | Prefix hits / tester input tokens |
| --- | --- | --- | --- |
| A: engine waiting + Router in-flight (as merged) | 13.1 / 14.4 s | 410 / 402 | 37% / 47% |
| B: engine waiting + running | 27.5 / 34.3 s | 351 / 307 | 30% / 32% |
| C: B + Router requests started after the last scrape | 23.1 / 20.8 s | 348 / 371 | 35% / 37% |

Under uniform load all three are within noise. B and C even out the engines' totals and send more Router traffic
into replica 2's queue. These runs can't separate two causes: request counts not reflecting work (each bypass
request decodes 1,024 tokens) and sessions losing their cached prefixes when they move.
