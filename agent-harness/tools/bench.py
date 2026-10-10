#!/usr/bin/env python3
"""Warm throughput check via Ollama's native API. Usage: python3 bench.py model [runs]"""
import json, sys, urllib.request
m = sys.argv[1]; runs = int(sys.argv[2]) if len(sys.argv) > 2 else 2
def one():
    body = json.dumps({"model": m, "prompt": "Write a 300 word explanation of how a hash map works.", "stream": False}).encode()
    with urllib.request.urlopen(urllib.request.Request("http://127.0.0.1:11434/api/generate", data=body), timeout=600) as r:
        return json.load(r)
one()  # warm-up / load
for _ in range(runs):
    r = one()
    print(f"{m:14s} prompt {r['prompt_eval_count']/(r['prompt_eval_duration']/1e9):7.0f} tok/s   gen {r['eval_count']/(r['eval_duration']/1e9):6.1f} tok/s   ({r['eval_count']} tokens, {r['total_duration']/1e9:.1f}s total)")
