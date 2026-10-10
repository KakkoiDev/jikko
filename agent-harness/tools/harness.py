#!/usr/bin/env python3
"""
Minimal tool-using agent harness against a local Ollama model, built for observability.

Every turn is appended to runs/<session>.jsonl as structured events:
  request       what was sent (message count, approx context size)
  response      thinking, content, tool_calls, token counts, timings
  tool_call     name, arguments, result (truncated), duration, exit code
  final         the model's last message, totals

Usage:
  python3 harness.py "Build a word-count CLI in Python with a test, and run the test"
  python3 harness.py --model gpt-oss:20b --workdir ./sandbox --max-turns 40 "..."
  python3 harness.py --no-think "..."          # disable thinking (faster, less insight)

The model can only read/write/run inside --workdir (default ./sandbox).
run_shell executes model-written commands in that directory with a timeout.
Only use on a machine where that is acceptable.
"""
import argparse, json, os, subprocess, sys, time, urllib.request, uuid
from pathlib import Path

OLLAMA = os.environ.get("OLLAMA_HOST", "http://127.0.0.1:11434")

# ----------------------------------------------------------------------------- tools
TOOLS = [
    {"type": "function", "function": {
        "name": "list_dir", "description": "List files and directories under a relative path in the workspace.",
        "parameters": {"type": "object", "properties": {"path": {"type": "string", "description": "Relative path, '.' for the root"}}, "required": ["path"]}}},
    {"type": "function", "function": {
        "name": "read_file", "description": "Read a UTF-8 text file in the workspace.",
        "parameters": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}}},
    {"type": "function", "function": {
        "name": "write_file", "description": "Create or overwrite a text file in the workspace. Parent directories are created.",
        "parameters": {"type": "object", "properties": {"path": {"type": "string"}, "content": {"type": "string"}}, "required": ["path", "content"]}}},
    {"type": "function", "function": {
        "name": "run_shell", "description": "Run a shell command inside the workspace and return stdout, stderr and exit code. 60 second timeout.",
        "parameters": {"type": "object", "properties": {"command": {"type": "string"}}, "required": ["command"]}}},
]

class Workspace:
    def __init__(self, root: Path):
        self.root = root.resolve(); self.root.mkdir(parents=True, exist_ok=True)
    def _safe(self, rel: str) -> Path:
        p = (self.root / rel).resolve()
        if p != self.root and self.root not in p.parents:
            raise ValueError(f"path escapes workspace: {rel}")
        return p
    def list_dir(self, path="."):
        p = self._safe(path)
        if not p.exists(): return f"not found: {path}"
        return "\n".join(sorted(f"{e.name}/" if e.is_dir() else e.name for e in p.iterdir())) or "(empty)"
    def read_file(self, path):
        p = self._safe(path)
        if not p.is_file(): return f"not a file: {path}"
        return p.read_text(errors="replace")[:20000]
    def write_file(self, path, content):
        p = self._safe(path); p.parent.mkdir(parents=True, exist_ok=True); p.write_text(content)
        return f"wrote {len(content)} chars to {path}"
    def run_shell(self, command):
        try:
            r = subprocess.run(command, shell=True, cwd=self.root, capture_output=True, text=True, timeout=60)
            out = f"exit={r.returncode}\nstdout:\n{r.stdout[-6000:]}\nstderr:\n{r.stderr[-3000:]}"
            return out, r.returncode
        except subprocess.TimeoutExpired:
            return "exit=timeout\ncommand exceeded 60s", -1

# ----------------------------------------------------------------------------- logging
class Log:
    def __init__(self, path: Path):
        path.parent.mkdir(parents=True, exist_ok=True)
        self.path = path; self.f = path.open("a"); self.t0 = time.time()
    def emit(self, kind, **data):
        ev = {"t": round(time.time() - self.t0, 3), "kind": kind, **data}
        self.f.write(json.dumps(ev, ensure_ascii=False) + "\n"); self.f.flush()
        return ev

# ----------------------------------------------------------------------------- model
def chat(model, messages, think, num_ctx):
    body = {"model": model, "messages": messages, "tools": TOOLS, "stream": False,
            "options": {"num_ctx": num_ctx}}
    if think is not None: body["think"] = think
    req = urllib.request.Request(f"{OLLAMA}/api/chat", data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    t = time.time()
    with urllib.request.urlopen(req, timeout=900) as r:
        out = json.load(r)
    out["_wall"] = round(time.time() - t, 2)
    return out

SYSTEM = """You are a careful software engineer working inside a sandbox workspace.
Use the tools to inspect, create and run code. Work in small steps: look before you write, run tests after you write.
When the task is complete and verified, reply with a short summary and stop calling tools."""

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("task")
    ap.add_argument("--model", default="gemma4:26b")
    ap.add_argument("--workdir", default="sandbox")
    ap.add_argument("--max-turns", type=int, default=30)
    ap.add_argument("--num-ctx", type=int, default=32768)
    ap.add_argument("--no-think", action="store_true")
    ap.add_argument("--runs-dir", default="runs")
    a = ap.parse_args()

    session = time.strftime("%Y%m%d-%H%M%S") + "-" + uuid.uuid4().hex[:6]
    log = Log(Path(a.runs_dir) / f"{session}.jsonl")
    ws = Workspace(Path(a.workdir))
    think = None if a.no_think else True
    messages = [{"role": "system", "content": SYSTEM}, {"role": "user", "content": a.task}]
    log.emit("session", id=session, model=a.model, task=a.task, workdir=str(ws.root), think=think, num_ctx=a.num_ctx)
    print(f"session {session} -> {log.path}")

    totals = {"prompt_tokens": 0, "completion_tokens": 0, "tool_calls": 0, "model_seconds": 0.0}
    for turn in range(1, a.max_turns + 1):
        log.emit("request", turn=turn, n_messages=len(messages), approx_chars=sum(len(json.dumps(m)) for m in messages))
        try:
            resp = chat(a.model, messages, think, a.num_ctx)
        except Exception as e:
            log.emit("error", turn=turn, error=repr(e)); print("model error:", e); break
        msg = resp["message"]
        calls = msg.get("tool_calls") or []
        ev = log.emit("response", turn=turn, thinking=msg.get("thinking"), content=msg.get("content"),
                      tool_calls=calls, prompt_tokens=resp.get("prompt_eval_count"),
                      completion_tokens=resp.get("eval_count"),
                      prompt_tps=round(resp.get("prompt_eval_count", 0) / max(resp.get("prompt_eval_duration", 1), 1) * 1e9, 1),
                      gen_tps=round(resp.get("eval_count", 0) / max(resp.get("eval_duration", 1), 1) * 1e9, 1),
                      wall=resp["_wall"])
        totals["prompt_tokens"] += resp.get("prompt_eval_count") or 0
        totals["completion_tokens"] += resp.get("eval_count") or 0
        totals["model_seconds"] += resp["_wall"]
        if msg.get("thinking"): print(f"\n[turn {turn} thinking] {msg['thinking'][:300].strip()}{'…' if len(msg['thinking'])>300 else ''}")
        if msg.get("content"): print(f"[turn {turn}] {msg['content'].strip()[:600]}")
        messages.append({k: v for k, v in msg.items() if k in ("role", "content", "tool_calls", "thinking")})
        if not calls:
            log.emit("final", turn=turn, content=msg.get("content"), **totals); break
        for c in calls:
            fn = c["function"]["name"]; args = c["function"].get("arguments") or {}
            if isinstance(args, str):
                try: args = json.loads(args)
                except json.JSONDecodeError: args = {"_raw": args}
            t = time.time(); code = None
            try:
                if fn == "run_shell": result, code = ws.run_shell(**args)
                elif fn in ("list_dir", "read_file", "write_file"): result = getattr(ws, fn)(**args)
                else: result = f"unknown tool {fn}"
            except Exception as e:
                result = f"tool error: {e!r}"
            totals["tool_calls"] += 1
            log.emit("tool_call", turn=turn, name=fn, arguments=args, result=result[:4000], exit_code=code, wall=round(time.time() - t, 3))
            print(f"  -> {fn}({json.dumps(args)[:120]}) {'exit='+str(code) if code is not None else ''}")
            messages.append({"role": "tool", "tool_name": fn, "content": result})
    else:
        log.emit("final", turn=a.max_turns, content=None, stopped="max_turns", **totals)
    print(f"\ndone. {totals}  log: {log.path}")

if __name__ == "__main__":
    main()
