// Observer extension: per-turn telemetry for experiments (spec/02-observability.md).
//
// Pi's session file already holds the full transcript: system prompt sections,
// every user/assistant/tool message, thinking blocks, tool calls, usage. This
// extension adds what the transcript lacks, as one JSONL file per session next
// to the session file (<session-dir>/telemetry/<session-id>.jsonl):
//   - wall-clock duration per turn and per run
//   - context usage as the harness sees it after each turn
//   - derived sizes (thinking chars, text chars, tool-call count) so the
//     analyzer does not have to re-walk content blocks
//   - tool errors as they happen
// It never changes prompts, tools, or results.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { appendFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";

type Ctx = any; // ExtensionContext; kept loose so the file survives minor API drift

export default function observer(pi: ExtensionAPI) {
  let runStartedAt = 0;
  let turnStartedAt = 0;
  let turnIndex = 0;

  const emit = (ctx: Ctx, kind: string, data: Record<string, unknown>) => {
    const sessionFile: string | undefined = ctx.sessionManager?.getSessionFile?.();
    if (!sessionFile) return; // --no-session: nothing durable to attach to
    const dir = join(dirname(sessionFile), "telemetry");
    mkdirSync(dir, { recursive: true });
    const line = JSON.stringify({ ts: new Date().toISOString(), kind, session: ctx.sessionManager.getSessionId(), ...data });
    appendFileSync(join(dir, `${ctx.sessionManager.getSessionId()}.jsonl`), line + "\n");
  };

  pi.on("session_start", async (ev: any, ctx: Ctx) => {
    turnIndex = 0;
    emit(ctx, "session_start", {
      reason: ev.reason,
      cwd: ctx.cwd,
      model: ctx.model ? `${ctx.model.provider}/${ctx.model.id}` : null,
      thinkingLevel: ctx.thinkingLevel ?? null,
      systemPromptChars: safeLen(() => ctx.getSystemPrompt()),
    });
  });

  pi.on("before_agent_start", async (ev: any, ctx: Ctx) => {
    runStartedAt = Date.now();
    emit(ctx, "run_start", { promptChars: ev.prompt?.length ?? 0, systemPromptChars: ev.systemPrompt?.length ?? 0 });
  });

  pi.on("turn_start", async (_ev: any, _ctx: Ctx) => {
    turnStartedAt = Date.now();
  });

  pi.on("turn_end", async (ev: any, ctx: Ctx) => {
    turnIndex = typeof ev.turnIndex === "number" ? ev.turnIndex + 1 : turnIndex + 1; // 1-based, like the analyzer
    const m = ev.message ?? {};
    const blocks: any[] = Array.isArray(m.content) ? m.content : [];
    const thinking = blocks.filter((b) => b.type === "thinking").map((b) => b.thinking ?? "").join("");
    const text = blocks.filter((b) => b.type === "text").map((b) => b.text ?? "").join("");
    const calls = blocks.filter((b) => b.type === "toolCall").map((b) => b.name);
    const results: any[] = ev.toolResults ?? [];
    emit(ctx, "turn", {
      turn: turnIndex,
      model: m.model ?? null,
      stopReason: m.stopReason ?? null,
      thinkingChars: thinking.length,
      textChars: text.length,
      toolCalls: calls,
      toolErrors: results.filter((r) => r.isError).map((r) => r.toolName),
      usage: m.usage ?? null,
      context: safe(() => ctx.getContextUsage()),
      wallMs: turnStartedAt ? Date.now() - turnStartedAt : null,
    });
  });

  pi.on("tool_result", async (ev: any, ctx: Ctx) => {
    if (!ev.isError) return;
    const text = (ev.content ?? []).filter((c: any) => c.type === "text").map((c: any) => c.text).join("\n");
    emit(ctx, "tool_error", { tool: ev.toolName, input: ev.input, error: text.slice(0, 500) });
  });

  pi.on("agent_end", async (ev: any, ctx: Ctx) => {
    emit(ctx, "run_end", {
      turns: turnIndex,
      wallMs: runStartedAt ? Date.now() - runStartedAt : null,
      messages: ev.messages?.length ?? 0,
    });
  });
}

function safe<T>(f: () => T): T | null {
  try { return f() ?? null; } catch { return null; }
}
function safeLen(f: () => string): number | null {
  try { return f()?.length ?? null; } catch { return null; }
}
