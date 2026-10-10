// Jikko extension (spec/03-jikko-integration.md, spec/04-pi-mapping.md).
//
// Exposes the Jikko workspace to the model as tools that mirror the CLI,
// pre-loads the task page and its links into the first message, and commits
// the workspace at the end of the run. Active only when JIKKO_DIR and
// JIKKO_TOKEN are set, so run.sh can load it unconditionally.
//
// Environment:
//   JIKKO_DIR       workspace directory
//   JIKKO_TOKEN     credential of the identity the model runs as
//   JIKKO_IDENTITY  that identity's name (informational, goes in the first message)
//   JIKKO_TASK      task page reference, e.g. tasks/wc-cli (optional: no task, no pre-load)
//   JIKKO_BIN       jikko binary (default: "jikko" on PATH)
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";
import { spawnSync } from "node:child_process";
import { appendFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";

const DIR = process.env.JIKKO_DIR;
const TOKEN = process.env.JIKKO_TOKEN;
const IDENTITY = process.env.JIKKO_IDENTITY ?? "agent";
const TASK = process.env.JIKKO_TASK;
const BIN = process.env.JIKKO_BIN ?? "jikko";
const LINK_BUDGET_CHARS = 24_000; // roughly 8K tokens of linked pages in the first message

type Ctx = any;

function jikko(args: string[], stdin?: string): { ok: boolean; out: string } {
  const r = spawnSync(BIN, [...args, "--dir", DIR!], {
    env: { ...process.env, JIKKO_TOKEN: TOKEN },
    input: stdin,
    encoding: "utf8",
    maxBuffer: 16 * 1024 * 1024,
  });
  const out = (r.stdout ?? "") + (r.stderr ? (r.stdout ? "\n" : "") + r.stderr : "");
  return { ok: r.status === 0, out: out.trim() };
}

function text(s: string) {
  return { content: [{ type: "text" as const, text: s || "(no output)" }], details: undefined };
}

function run(args: string[], stdin?: string) {
  const r = jikko(args, stdin);
  if (!r.ok) throw new Error(`jikko ${args[0]} failed:\n${r.out}`);
  return text(r.out);
}

// Jikko stores a comment as one paragraph with no code fences: a blank line
// would split it into two messages and a fence would swallow the thread
// markup. Fold blank lines and turn fenced blocks into inline code so a
// list, two sentences, or a short snippet still land as one comment.
function commentText(message: string): string {
  const fenced = message.replace(/```[^\n]*\n([\s\S]*?)```/g, (_m, body: string) =>
    "`" + body.trim().split("\n").map((l: string) => l.trim()).filter(Boolean).join(" ; ") + "`");
  return fenced.replace(/\n[ \t]*\n+/g, "\n").trim();
}

function log(ctx: Ctx, data: Record<string, unknown>) {
  try {
    const f = ctx.sessionManager?.getSessionFile?.();
    if (!f) return;
    const dir = join(dirname(f), "telemetry");
    mkdirSync(dir, { recursive: true });
    appendFileSync(join(dir, `${ctx.sessionManager.getSessionId()}.jsonl`),
      JSON.stringify({ ts: new Date().toISOString(), kind: "jikko", session: ctx.sessionManager.getSessionId(), ...data }) + "\n");
  } catch { /* telemetry is best effort */ }
}

export default function jikkoExtension(pi: ExtensionAPI) {
  if (!DIR || !TOKEN) return; // not a Jikko run

  const ref = Type.String({ description: "Page reference: path without .md, e.g. tasks/wc-cli, or a unique title" });

  pi.registerTool({
    name: "jikko_tree", label: "jikko tree",
    description: "List every page in the Jikko workspace you are allowed to read (path, title, type).",
    parameters: Type.Object({}),
    async execute() { return run(["tree", "--json"]); },
  });

  pi.registerTool({
    name: "jikko_show", label: "jikko show",
    description: "Read one or more Jikko pages: metadata, body, links. Use before acting on anything you have not read.",
    parameters: Type.Object({ refs: Type.Array(ref, { minItems: 1 }) }),
    async execute(_id, p) { return run(["show", ...p.refs, "--json"]); },
  });

  pi.registerTool({
    name: "jikko_mentions", label: "jikko mentions",
    description: "Your inbox: pages that mention you or a group you belong to.",
    parameters: Type.Object({}),
    async execute() { return run(["mentions", "--json"]); },
  });

  pi.registerTool({
    name: "jikko_view", label: "jikko view",
    description: "Evaluate a saved view page (a query over the workspace).",
    parameters: Type.Object({ ref }),
    async execute(_id, p) { return run(["view", p.ref, "--json"]); },
  });

  pi.registerTool({
    name: "jikko_comment", label: "jikko comment",
    description: "Discuss on a page. add: new comment anchored on a quoted span of the page text (use a short exact phrase from the page, e.g. the title). reply: answer an existing comment id. list: comments on a page. Resolving threads is the reviewer's job, not yours.",
    parameters: Type.Object({
      action: Type.Union([Type.Literal("add"), Type.Literal("reply"), Type.Literal("list")]),
      ref,
      anchor: Type.Optional(Type.String({ description: "add: exact text from the page to attach the comment to" })),
      id: Type.Optional(Type.String({ description: "reply: the comment id, e.g. c3" })),
      message: Type.Optional(Type.String({ description: "add/reply: one Markdown paragraph (single newlines allowed, no blank lines). @mention people for attention." })),
    }),
    promptGuidelines: ["Post a plan comment before writing code, progress comments as you go, and a result comment with evidence at the end."],
    async execute(_id, p) {
      const msg = commentText(p.message ?? "");
      switch (p.action) {
        case "add": return run(["comment", "add", p.ref, p.anchor ?? "", msg, "--json"]);
        case "reply": return run(["comment", "reply", p.ref, p.id ?? "", msg, "--json"]);
        case "list": return run(["comment", "list", p.ref, "--json"]);
      }
    },
  });

  pi.registerTool({
    name: "jikko_set", label: "jikko set",
    description: "Set one metadata property on a page, e.g. a task's status to doing, blocked, or done.",
    parameters: Type.Object({ ref, property: Type.String(), value: Type.String() }),
    async execute(_id, p) { return run(["set", p.ref, p.property, p.value]); },
  });

  pi.registerTool({
    name: "jikko_create", label: "jikko create",
    description: "Create a new document or task page. Only when the task explicitly asks for a new page.",
    parameters: Type.Object({
      path: Type.String({ description: "e.g. docs/notes-on-x (no .md)" }),
      type: Type.Union([Type.Literal("document"), Type.Literal("task")]),
      body: Type.String({ description: "Markdown body starting with a # title" }),
    }),
    async execute(_id, p) { return run(["create", p.path, "--type", p.type, "--body", p.body]); },
  });

  pi.registerTool({
    name: "jikko_save", label: "jikko save",
    description: "Replace a page's full source (frontmatter + body) with Jikko's merge and validation. Read it with jikko_show first and pass that exact source as base.",
    parameters: Type.Object({
      ref,
      source: Type.String({ description: "the complete new Markdown source" }),
      base: Type.String({ description: "the source as you read it, for conflict detection" }),
    }),
    async execute(_id, p) {
      return run(["save", p.ref, "--file", "-", "--base", p.base, "--json"], p.source);
    },
  });

  // First message: who you are, the task, and what it links to.
  pi.on("before_agent_start", async (_ev: any, ctx: Ctx) => {
    if (!TASK) return;
    const task = jikko(["show", TASK, "--json"]);
    if (!task.ok) {
      log(ctx, { step: "preload", error: task.out });
      return { message: { customType: "jikko-context", display: true, content: `You are ${IDENTITY} in the Jikko workspace at ${DIR}. The task ${TASK} could not be read:\n${task.out}` } };
    }
    let pages: any[] = [];
    try { pages = JSON.parse(task.out); } catch { pages = []; }
    const links: string[] = [...new Set(pages.flatMap((p) => p.links ?? []))];
    let linked = "";
    let budget = LINK_BUDGET_CHARS;
    const skipped: string[] = [];
    for (const l of links) {
      const r = jikko(["show", l, "--json"]);
      if (!r.ok) { skipped.push(l); continue; }
      if (r.out.length > budget) { skipped.push(l); continue; }
      linked += `\n\n### ${l}\n${r.out}`;
      budget -= r.out.length;
    }
    const body = [
      `You are **${IDENTITY}** in the Jikko workspace at \`${DIR}\`. Your task is **${TASK}**. Code goes in the current working directory \`${ctx.cwd}\`; the record of the work goes to Jikko through the jikko_* tools.`,
      `\n## Task page (jikko show ${TASK})\n${task.out}`,
      links.length ? `\n## Linked pages${linked}` : "",
      skipped.length ? `\nNot pre-loaded (fetch with jikko_show if needed): ${skipped.join(", ")}` : "",
    ].join("\n");
    log(ctx, { step: "preload", task: TASK, links, skipped, chars: body.length });
    return { message: { customType: "jikko-context", display: true, content: body } };
  });

  // Run end: one commit per run, then a workspace check.
  pi.on("agent_end", async (_ev: any, ctx: Ctx) => {
    const sid = ctx.sessionManager?.getSessionId?.() ?? "unknown";
    const msg = `${IDENTITY}: ${TASK ?? "session"} (pi session ${sid})`;
    const commit = jikko(["commit", "--operation", "agent-turn", msg]);
    const check = jikko(["check"]);
    log(ctx, { step: "commit", ok: commit.ok, out: commit.out.slice(0, 500), check_ok: check.ok, check: check.out.slice(0, 500) });
    if (ctx.hasUI) ctx.ui.notify(`jikko commit: ${commit.ok ? "ok" : "failed"}; check: ${check.out.split("\n")[0]}`, commit.ok && check.ok ? "info" : "warning");
  });
}
