"""Summarize arms: requests, input tokens, TTFT, Router split, replica queues, load_aware pick outcomes.

The Router split comes from router.log, or from router_split.json in a trimmed result directory.

Usage: report.py RESULT_DIR [RESULT_DIR ...]
"""

import json
import sys
from collections import Counter, defaultdict
from pathlib import Path

import pandas as pd


def router_split(d):
    saved = d / "router_split.json"
    if saved.exists():
        return Counter(json.loads(saved.read_text()))
    log = d / "router.log"
    split = Counter()
    for line in log.read_text(errors="replace").splitlines():
        if '"msg":"access"' not in line:
            continue
        try:
            host = json.loads(line).get("upstream_host", "")
        except json.JSONDecodeError:
            continue
        if host:
            split[host.rsplit(":", 1)[-1]] += 1
    return split


def samples(path):
    waiting = defaultdict(list)
    outcomes = {}
    for line in path.read_text().splitlines():
        parts = line.split()
        if len(parts) != 4:
            continue
        _, who, name, value = parts
        if who.startswith("replica") and name.endswith("waiting"):
            waiting[who].append(float(value))
        elif who == "router":
            outcomes[name.split('outcome="')[-1].rstrip('"}')] = float(value)
    return waiting, outcomes


def main():
    rows = []
    for d in map(Path, sys.argv[1:]):
        df = pd.read_csv(d / "detailed_results.csv")
        ok = df[df.success == True]  # noqa: E712
        ttft = ok.effective_ttft
        split = router_split(d)
        waiting, outcomes = samples(d / "samples.txt")
        rows.append({
            "arm": d.name,
            "requests": len(ok),
            "failed": int((df.success != True).sum()),  # noqa: E712
            "input_Mtok": round(ok.input_tokens.sum() / 1e6, 2),
            "ttft_p50": round(ttft.quantile(0.5), 2),
            "ttft_p90": round(ttft.quantile(0.9), 2),
            "ttft_p99": round(ttft.quantile(0.99), 2),
            "ttft_max": round(ttft.max(), 2),
            "router_to_r1/r2": f"{split.get('18751', 0)}/{split.get('18752', 0)}",
            "mean_wait_r1/r2": "/".join(
                f"{sum(v) / len(v):.1f}" if v else "-" for v in (waiting["replica1"], waiting["replica2"])
            ),
            "picks": " ".join(f"{k}={int(v)}" for k, v in sorted(outcomes.items())) or "-",
        })
    print(pd.DataFrame(rows).to_markdown(index=False))


if __name__ == "__main__":
    main()
