#!/usr/bin/env python3
"""
Summarize one or more harness runs.

  python3 analyze.py runs/*.jsonl            # one summary row per run
  python3 analyze.py runs/<id>.jsonl --trace # turn-by-turn: thinking excerpt, tool, result
  python3 analyze.py runs/<id>.jsonl --thinking 5   # full thinking of turn 5
"""
import argparse, json, sys
from collections import Counter
from pathlib import Path

def load(p):
    return [json.loads(l) for l in Path(p).read_text().splitlines() if l.strip()]

def summary(events):
    s = next(e for e in events if e["kind"] == "session")
    resp = [e for e in events if e["kind"] == "response"]
    tools = [e for e in events if e["kind"] == "tool_call"]
    final = next((e for e in events if e["kind"] == "final"), {})
    think_chars = sum(len(e.get("thinking") or "") for e in resp)
    fails = sum(1 for t in tools if (t.get("exit_code") not in (None, 0)) or str(t["result"]).startswith(("tool error", "not found", "not a file")))
    return {
        "run": s["id"], "model": s["model"], "turns": len(resp), "tool_calls": len(tools),
        "tool_fail": fails, "by_tool": dict(Counter(t["name"] for t in tools)),
        "prompt_tok": final.get("prompt_tokens"), "gen_tok": final.get("completion_tokens"),
        "think_chars": think_chars, "model_s": round(final.get("model_seconds") or 0, 1),
        "wall_s": round(events[-1]["t"], 1), "stopped": final.get("stopped", "ok" if final else "crashed"),
        "last_ctx_tok": resp[-1].get("prompt_tokens") if resp else None,
        "task": s["task"][:60],
    }

def trace(events):
    for e in events:
        k = e["kind"]
        if k == "response":
            th = (e.get("thinking") or "").strip().replace("\n", " ")
            print(f"\n== turn {e['turn']}  ctx={e.get('prompt_tokens')} tok  gen={e.get('completion_tokens')} tok  {e['wall']}s  ({e.get('gen_tps')} tok/s)")
            if th: print(f"   think: {th[:240]}{'…' if len(th) > 240 else ''}")
            if e.get("content"): print(f"   say:   {e['content'].strip()[:240]}")
        elif k == "tool_call":
            r = str(e["result"]).replace("\n", " | ")
            print(f"   -> {e['name']} {json.dumps(e['arguments'], ensure_ascii=False)[:160]}")
            print(f"      {r[:200]}")
        elif k == "error":
            print(f"!! error: {e['error']}")

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("files", nargs="+")
    ap.add_argument("--trace", action="store_true")
    ap.add_argument("--thinking", type=int, metavar="TURN")
    a = ap.parse_args()
    if a.trace or a.thinking:
        ev = load(a.files[0])
        if a.trace: trace(ev)
        if a.thinking:
            e = next(x for x in ev if x["kind"] == "response" and x["turn"] == a.thinking)
            print(e.get("thinking") or "(no thinking recorded)")
        return
    rows = [summary(load(f)) for f in a.files]
    cols = ["run", "model", "turns", "tool_calls", "tool_fail", "prompt_tok", "gen_tok", "think_chars", "model_s", "wall_s", "last_ctx_tok", "stopped"]
    print("  ".join(f"{c:>12}" for c in cols))
    for r in rows:
        print("  ".join(f"{str(r[c])[:12]:>12}" for c in cols))
    for r in rows:
        print(f"{r['run']}: tools={r['by_tool']}  task={r['task']!r}")

if __name__ == "__main__":
    main()
