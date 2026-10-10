#!/usr/bin/env python3
"""
Analyze Pi session files (spec/02-observability.md).

  pi_analyze.py runs/pi/*.jsonl                 one summary row per session
  pi_analyze.py --trace <session.jsonl>         turn by turn: thinking, calls, results
  pi_analyze.py --thinking N <session.jsonl>    full thinking text of turn N
  pi_analyze.py --json runs/pi/*.jsonl          summaries as JSON lines

Reads the session format Pi writes (version 2/3 tree of entries). If a
telemetry file exists next to it (telemetry/<id>.jsonl, written by
pi/observer.ts) wall-clock and context figures come from there.
"""
import argparse, json, sys
from collections import Counter
from pathlib import Path


def load(path):
    return [json.loads(l) for l in Path(path).read_text().splitlines() if l.strip()]


def telemetry_for(path, session_id):
    t = Path(path).parent / "telemetry" / f"{session_id}.jsonl"
    return load(t) if t.exists() else []


def ctx_tokens(t):
    c = t.get("context") or {}
    for k in ("tokens", "totalTokens", "used", "input"):
        if isinstance(c.get(k), (int, float)):
            return int(c[k])
    return None


def blocks(msg, kind):
    c = msg.get("content")
    return [b for b in c if b.get("type") == kind] if isinstance(c, list) else []


def turns(entries):
    """Yield (assistant message entry, [tool result entries]) along the file order."""
    out, cur = [], None
    for e in entries:
        if e.get("type") != "message":
            continue
        m = e["message"]
        if m["role"] == "assistant":
            cur = (e, [])
            out.append(cur)
        elif m["role"] == "toolResult" and cur:
            cur[1].append(e)
    return out


def summary(path):
    entries = load(path)
    header = entries[0]
    sid = header.get("id")
    tel = telemetry_for(path, sid)
    ts = [e for e in turns(entries)]
    users = [e for e in entries if e.get("type") == "message" and e["message"]["role"] == "user"]
    task = users[0]["message"]["content"] if users else ""
    if isinstance(task, list):
        task = " ".join(b.get("text", "") for b in task)
    calls = [b["name"] for e, _ in ts for b in blocks(e["message"], "toolCall")]
    errors = sum(1 for _, rs in ts for r in rs if r["message"].get("isError"))
    think = sum(len(b.get("thinking", "")) for e, _ in ts for b in blocks(e["message"], "thinking"))
    usage = [e["message"].get("usage") or {} for e, _ in ts]
    model = next((e["message"].get("model") for e, _ in ts if e["message"].get("model")), header.get("model"))
    stamps = [e["message"].get("timestamp") for e, _ in ts] + [u["message"].get("timestamp") for u in users]
    stamps = [s for s in stamps if isinstance(s, (int, float))]
    run_end = next((t for t in tel if t["kind"] == "run_end"), None)
    # With Ollama, usage.input counts only uncached prompt tokens, so the last
    # assistant's input is not the context size. Prefer the observer's figure.
    tturns = [t for t in tel if t["kind"] == "turn"]
    ctx_last = ctx_tokens(tturns[-1]) if tturns else None
    return {
        "session": sid[:8] if sid else "?",
        "model": model,
        "turns": len(ts),
        "tool_calls": len(calls),
        "tool_err": errors,
        "by_tool": dict(Counter(calls)),
        "in_tok": sum(u.get("input", 0) for u in usage),
        "out_tok": sum(u.get("output", 0) for u in usage),
        "think_chars": think,
        "last_ctx": ctx_last if ctx_last is not None else (usage[-1].get("input") if usage else None),
        "wall_s": round((run_end["wallMs"] / 1000), 1) if run_end and run_end.get("wallMs") else (round((max(stamps) - min(stamps)) / 1000, 1) if len(stamps) > 1 else None),
        "stop": ts[-1][0]["message"].get("stopReason") if ts else "none",
        "task": task[:60].replace("\n", " "),
        "file": str(path),
    }


def trace(path):
    entries = load(path)
    tel = {t["turn"]: t for t in telemetry_for(path, entries[0].get("id")) if t["kind"] == "turn"}
    for i, (e, rs) in enumerate(turns(entries), 1):
        m = e["message"]; u = m.get("usage") or {}
        t = tel.get(i, {})
        ctx = ctx_tokens(t) if t else None
        print(f"\n== turn {i}  ctx={ctx if ctx is not None else str(u.get('input')) + ' uncached'} tok  out={u.get('output')} tok  {(t.get('wallMs') or 0)/1000:.1f}s  stop={m.get('stopReason')}")
        th = " ".join(b.get("thinking", "") for b in blocks(m, "thinking")).strip().replace("\n", " ")
        if th:
            print(f"   think: {th[:240]}{'…' if len(th) > 240 else ''}")
        tx = " ".join(b.get("text", "") for b in blocks(m, "text")).strip().replace("\n", " ")
        if tx:
            print(f"   say:   {tx[:240]}{'…' if len(tx) > 240 else ''}")
        for b in blocks(m, "toolCall"):
            print(f"   -> {b['name']} {json.dumps(b.get('arguments', {}), ensure_ascii=False)[:160]}")
        for r in rs:
            rm = r["message"]
            text = " | ".join(c.get("text", "") for c in rm.get("content", []) if c.get("type") == "text").replace("\n", " | ")
            flag = "ERR " if rm.get("isError") else ""
            print(f"      {flag}{text[:200]}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("files", nargs="+")
    ap.add_argument("--trace", action="store_true")
    ap.add_argument("--thinking", type=int, metavar="TURN")
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args()
    if a.trace:
        return trace(a.files[0])
    if a.thinking:
        ts = turns(load(a.files[0]))
        e, _ = ts[a.thinking - 1]
        print("\n".join(b.get("thinking", "") for b in blocks(e["message"], "thinking")) or "(no thinking recorded)")
        return
    rows = [summary(f) for f in a.files]
    if a.json:
        for r in rows:
            print(json.dumps(r, ensure_ascii=False))
        return
    cols = ["session", "model", "turns", "tool_calls", "tool_err", "in_tok", "out_tok", "think_chars", "last_ctx", "wall_s", "stop"]
    print("  ".join(f"{c:>11}" for c in cols))
    for r in rows:
        print("  ".join(f"{str(r[c])[:11]:>11}" for c in cols))
    for r in rows:
        print(f"{r['session']}: {r['by_tool']}  {r['task']!r}")


if __name__ == "__main__":
    main()
