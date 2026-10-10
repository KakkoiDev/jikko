# Design brief: Jikko web interface

For a designer (human or AI) asked to improve the look and feel of Jikko's
browser interface. Read this, then the constraints, then look at the current
screens. Deliverables are at the end.

## What Jikko is

Jikko is a workspace where humans and AI agents define, discuss, track, prove,
and audit work together. The source of truth is a folder of Markdown files;
the web interface is one window onto it, the CLI is another, Git is the audit
trail. Four page kinds exist:

| Kind | What it is | What matters on screen |
|---|---|---|
| Document | Prose, designs, notes. Links to other pages with `[[name]]`, embeds them with `![[name]]`. | Readable long-form text; links and embeds visibly distinct. |
| Task | Actionable work. Has `status` (todo, doing, blocked, done), `assignee`, optional `due`. | Status and owner at a glance; discussion right next to the text. |
| Identity | A person, an agent, or a group (an identity with `members`). | Who it is, whether it is here now, what it is doing, what it did. |
| View | A saved query over pages, rendered as a list, table, or board. | Dense, scannable, obviously derived rather than authored. |

Comments are inline: a comment is anchored to a span of the page text and shown
in a discussion rail beside it. `@mentions` ask for attention; `assignee` is
responsibility. Agents post plans, progress, questions, and results as comments,
and move task status as they go. Everything an agent does is one Git commit.

## Who uses it

- **The owner**, a developer running one or more local AI agents on a Mac mini.
  Opens the workspace several times a day to hand out tasks, answer the
  agents' questions, review results, and resolve comment threads.
- **The agents** never see the interface. They read and write through the CLI.
  But their activity is what the owner wants to see: who is running, idle or
  working, on which task, since when, and what they produced.
- Later: a small team sharing one workspace, where identities and permissions
  matter and more than one person may be "here".

## What exists today

A single-column list of all pages with type filters, a login box that takes a
bearer token, page detail with front-matter fields, rendered Markdown, and a
discussion rail; an edit form (fields or raw source) with a three-way conflict
resolver; views rendered as list, table, or board.

Added in the latest iteration and in need of a designer's eye:

- A **"Who's here" rail** on the home page: running agents (green dot working,
  amber idle, grey offline) and logged-in people (blue dot), each with a
  one-line "what they're doing" and a link to their identity page.
- **Cards** in the page list now carry a kind badge, and tasks show a status
  pill, assignee, and due date. A text filter box narrows the list.
- **Identity pages** gained an activity section: Now (presence), Queue (todo
  tasks assigned), Recent comments, Commits.
- **Task pages** show assignee chips with presence.

All of it updates live over server-sent events.

Screens to look at: `/` (home), `/p/tasks/wc-cli.md` (a task with agent
comments), `/p/identity/gemma.md` (an agent identity), `/edit/tasks/wc-cli.md`
(the editor), and a view page if one exists.

## Problems to solve, in priority order

1. **The home page does not answer "what needs me?"** Tasks blocked on a
   question, results waiting for review, and agents sitting idle are buried in
   a flat list. The owner should see those first, every time.
2. **Agent activity is legible but not glanceable.** Dots and one-liners work;
   a sense of motion is missing. Something should make "two agents working,
   one idle for an hour" obvious without reading.
3. **Discussion is the product and it looks like an afterthought.** Comment
   threads are plain cards; anchors are highlighted in yellow in the body.
   Threads need author identity (human vs agent), time, and a clearer link
   between anchor and thread. Replying and resolving should feel light.
4. **Task state is text.** Status should be a control for people who may write
   the page, not only a badge, and the transition todo → doing → done should be
   visible in the page's history.
5. **Hierarchy and typography.** Basecoat gives sensible defaults; the page
   needs a type scale, spacing rhythm, and a header that carries navigation
   (home, tasks, identities, views) and the current user.
6. **Empty and first-run states.** A new workspace shows "No pages." and
   "Nobody right now." These are the moments to teach what Jikko is.
7. **Dark mode and small screens.** Nothing is designed for either yet.

## Constraints

- **Server-rendered HTML with HTMX.** Go templates in `web/templates/ui.html`,
  HTMX 4 for partial swaps and server-sent events. No build step, no React, no
  bundler. JavaScript is progressive enhancement only, in `web/assets/jikko.js`.
- **Basecoat CSS** (`web/assets/basecoat.min.css`) for primitives (buttons,
  inputs, cards, badges, alerts). Jikko's own styles are the ~150 lines in
  `web/assets/jikko.css`. Prefer extending that file to introducing a
  framework.
- **Every action works without JavaScript.** Forms post, pages reload. HTMX
  makes it smoother, never necessary.
- **Markdown stays the source.** Nothing the design adds may need data that is
  not in the Markdown files, Git history, or the derived presence records.
  Derived data lives in `.data/` and may be lost without harm.
- **Permissions filter everything.** A page the viewer may not read never
  appears, not even as a name in a list. Presence and activity obey this too.
- **Accessibility.** Keyboard-reachable controls, visible focus, real buttons
  and links, `aria-current` on active filters, color never the only signal
  (every dot has a text state next to it).
- **Local first.** Runs on `127.0.0.1` with no CDN, no web fonts unless
  vendored, no analytics.

## Design principles to hold

- Calm. The owner checks in; the page should not shout. Motion only where it
  means something (an agent working).
- Text first. It is a Markdown tool; the rendered page is the hero.
- Honest about agents. An agent is an identity like a person, with the same
  page shape, but it should never be mistaken for a human in a thread.
- Everything links. Tasks to assignees, comments to anchors, identities to
  what they are doing, commits to the pages they changed.

## Deliverables

1. A short written critique of the current screens against the problems above.
2. Mockups (static HTML/CSS is ideal, since it can be lifted straight into the
   templates) for: home, task detail with discussion, identity detail with
   activity, and the "Who's here" rail in both populated and empty states.
3. A type scale, spacing scale, and color tokens (light and dark) expressed as
   CSS custom properties that fit on top of Basecoat's.
4. The discussion thread component: anchored comment, reply, resolve, author
   treatment for human vs agent, timestamps.
5. Recommendations for the status control on tasks and for surfacing "needs
   me" on the home page, with the HTMX interaction sketched.

Keep proposals implementable in Go templates and plain CSS by one developer in
an afternoon each. Reference the template names in `ui.html` where a change
would land: `page`, `pages`, `here`, `detail`, `activity`, `viewresult`, `edit`.
