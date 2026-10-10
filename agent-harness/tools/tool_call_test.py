#!/usr/bin/env python3
"""Round-trip tool-calling test against a local OpenAI-compatible server.
Usage: python3 tool_call_test.py [model] [base_url]
Defaults: gpt-oss:20b at http://127.0.0.1:11434/v1
"""
import json, sys, time, urllib.request

MODEL = sys.argv[1] if len(sys.argv) > 1 else "gpt-oss:20b"
BASE = sys.argv[2] if len(sys.argv) > 2 else "http://127.0.0.1:11434/v1"

TOOLS = [{
    "type": "function",
    "function": {
        "name": "get_weather",
        "description": "Get the current weather for a city",
        "parameters": {
            "type": "object",
            "properties": {"city": {"type": "string", "description": "City name"}},
            "required": ["city"],
        },
    },
}]

def chat(messages):
    body = json.dumps({"model": MODEL, "messages": messages, "tools": TOOLS, "stream": False}).encode()
    req = urllib.request.Request(f"{BASE}/chat/completions", data=body,
                                 headers={"Content-Type": "application/json", "Authorization": "Bearer local"})
    t0 = time.time()
    with urllib.request.urlopen(req, timeout=600) as r:
        out = json.load(r)
    return out, time.time() - t0

messages = [{"role": "user", "content": "What is the weather in Tokyo right now? Use the tool."}]

# Turn 1: model should emit a tool_calls entry
resp, dt = chat(messages)
msg = resp["choices"][0]["message"]
print(f"turn 1 ({dt:.1f}s): finish_reason={resp['choices'][0].get('finish_reason')}")
calls = msg.get("tool_calls") or []
if not calls:
    print("NO tool_calls returned. Content was:", msg.get("content"))
    sys.exit(1)
call = calls[0]
args = json.loads(call["function"]["arguments"]) if isinstance(call["function"]["arguments"], str) else call["function"]["arguments"]
print("tool call:", call["function"]["name"], args)
assert call["function"]["name"] == "get_weather" and "city" in args, "schema mismatch"

# Turn 2: feed the tool result back, model should answer in prose
messages.append(msg)
messages.append({"role": "tool", "tool_call_id": call.get("id", "call_0"),
                 "content": json.dumps({"city": args["city"], "temp_c": 21, "sky": "light rain", "wind_kmh": 12})})
resp, dt = chat(messages)
final = resp["choices"][0]["message"].get("content", "")
print(f"turn 2 ({dt:.1f}s): {final.strip()}")
print("usage:", resp.get("usage"))
print("PASS")
