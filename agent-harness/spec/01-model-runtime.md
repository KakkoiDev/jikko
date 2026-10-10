# 01 — Model runtime

## Requirements on the endpoint

The agent harness talks to the model over an OpenAI-compatible Chat Completions
API. The endpoint must:

| Requirement | Why |
|---|---|
| Accept `tools` and return structured `tool_calls` (name plus JSON arguments), not tool calls rendered as text. | The loop cannot act on prose. The Apple on-device model fails this; see below. |
| Return reasoning separately from the answer (a `reasoning` field, or thinking content blocks). | Observability goal 2. Reasoning mixed into content cannot be analyzed apart from it. |
| Support a context window of at least 32K tokens, configurable upward. | A small project's files plus a few tool results exceed 8K quickly. |
| Report token usage per response. | Context-fill tracking and cost of thinking. |
| Run on this machine without network access. | Goal 5. |

Ollama's `/v1/chat/completions` meets all five. Its native `/api/chat` additionally
returns `thinking` as a first-class field and per-phase timings, which the
prototype used.

## Qualifying models (32 GB Mac mini, M6)

Memory rule: the whole model must be resident, so total size decides what fits.
Active parameters decide speed. Mixture-of-experts models are therefore the
right shape for this machine: 20 to 30B total, 3 to 4B active.

| Model | Disk | Active | Context | Tool round-trip | Warm generation | Notes |
|---|---|---|---|---|---|---|
| gemma4:26b | 16 GB | 3.8B | 256K | pass | 60 tok/s (MLX runner) | Default. Vision. Thinking returned separately. |
| gpt-oss:20b | 13 GB | 3.6B | 128K | pass | 38 tok/s (llama.cpp runner) | Verbose reasoning at medium effort (3 to 4K tokens for a 300-word answer). |
| qwen3-coder:30b | 19 GB | 3.3B | 256K | not yet tested | — | Built for agentic coding. Candidate for comparison. |
| apple-foundationmodel (via apfel or `fm serve`) | on-device | ~3B dense | 4K to 8K | **fail**: invents argument names | ~2 s per call | Keep for summarization and schema-constrained extraction only. |

Measured on 2026-10-10 with `tools/tool_call_test.py` and `tools/bench.py`.
Excluded: qwen3.6:35b (24 GB leaves no headroom), any dense 27B+ model
(bandwidth-bound to roughly 15 tok/s).

## Runtime settings

- Context: 32K is Ollama's default per model load. Runs that read several files
  should use 64K. Set with `OLLAMA_CONTEXT_LENGTH` on the server or `num_ctx`
  per request.
- Thinking: on for experiments (it is the point of observability), off for
  throughput comparisons. gpt-oss takes a reasoning level in the system prompt.
- Keep-alive: Ollama unloads a model after 5 minutes idle. Back-to-back
  experiments should keep one model loaded; alternating models costs a reload
  of 3 to 8 seconds each time.

## Re-validation

Any new model or Ollama upgrade must re-pass `tools/tool_call_test.py` before
it is used in an experiment. The test is two turns: a tool call with a
schema-matching argument, then a prose answer from the tool result.
