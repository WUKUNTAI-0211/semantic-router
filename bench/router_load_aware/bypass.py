"""Keep a fixed number of requests in flight against one replica, bypassing the Router.

Each prompt starts with a random token so it never hits the prefix cache.
Usage: bypass.py URL CONCURRENCY PROMPT_WORDS MAX_TOKENS [MODEL]
"""

import asyncio
import random
import sys

import aiohttp


async def worker(session, url, words, max_tokens, model, stop):
    while not stop.is_set():
        prompt = f"{random.random()} " + " ".join(["sea"] * words)
        body = {
            "model": model,
            "messages": [{"role": "user", "content": prompt}],
            "max_tokens": max_tokens,
            "ignore_eos": True,
        }
        try:
            async with session.post(url + "/v1/chat/completions", json=body) as r:
                await r.read()
        except aiohttp.ClientError:
            await asyncio.sleep(1)


async def main():
    url, conc, words, max_tokens = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])
    model = sys.argv[5] if len(sys.argv) > 5 else "Qwen/Qwen3-0.6B"
    stop = asyncio.Event()
    timeout = aiohttp.ClientTimeout(total=600)
    async with aiohttp.ClientSession(timeout=timeout) as session:
        await asyncio.gather(*(worker(session, url, words, max_tokens, model, stop) for _ in range(conc)))


if __name__ == "__main__":
    asyncio.run(main())
