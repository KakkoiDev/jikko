# Changelog

All notable changes to Jikko are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Jikko uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Until 1.0.0 a minor
version may change the CLI, the HTTP interface, or the file semantics.

## [Unreleased]

## [0.1.0] - Unreleased

The first tagged release: the v1 specification, the Go core and CLI, an HTTP
server with a browser interface, and a read-only browser/WASM runtime.
Set the date above when the tag is cut.

### Added

#### Format and specification

- The v1 specification: Markdown pages with YAML frontmatter, `[[links]]`,
  `![[embeds]]`, `@mentions`, and four kinds of page: Document (no type),
  Task, View, and Identity.
- A single Identity primitive for individuals and groups (`members:`), used
  for mentions, assignment, and authorization; `identity-permissions.md` and
  `permission-semantics-v1.md` define the authentication boundary and the
  `permissions` policy (`read`, `comment`, `write`, `admin`), open by default
  and fail-closed when a policy cannot be evaluated.
- The work model: Tasks name responsibility, dependencies, blockers, and
  evidence with `assignee`, `depends_on`, `blocked_by`, `proof`, and
  `requires`; Views are saved queries with `filter`, `sort`, and `group`
  plus board, table, and list layouts.
- Inline comments kept in the Markdown they discuss, with explicit source
  markers and an in-file thread.
- Apache 2.0 license and a security policy (`SECURITY.md`).

#### Go core

- Workspace loading that reports problems (malformed frontmatter, unknown
  types, broken references, membership cycles, unresolvable policies)
  instead of failing, honours byte order marks, and keeps one bad file from
  hiding the rest.
- Reference resolution for links, embeds, mentions, and asset embeds, with
  backlinks and transitive group mentions derived from the Markdown rather
  than stored.
- Token credentials hashed into harness-local `.auth.md`, with a warning when
  that file is tracked by Git.
- Authorized semantic mutations judged by their effect before anything is
  written: property `set`, policy `perm`, page creation, comment add, reply,
  and resolve, rename with reference rewriting, delete with dangling-reference
  reporting, and uploads bounded by a configurable size limit.
- Whole-page save with optimistic revision checks and a three-way merge
  (frontmatter key by key, body line by line) that returns structured
  conflicts instead of overwriting.
- Page composition from field edits, a safe Markdown renderer that escapes
  all source and treats Mermaid as text, and linear-time rendering and
  rewriting of large pages.
- Git commits with `Jikko-Actor` and `Jikko-Operation` audit trailers, and
  refusal of commit messages that could forge them.
- Portable ZIP export that excludes `.git/`, `.data/`, `.auth.md`, and
  symbolic links.

#### CLI (`jikko`)

- `list`, `show` (batch and `--raw`), `tree`, `view`, `mentions`, and `check`,
  each with `--json` for agents and scripts and all filtered by the
  authenticated identity.
- `create`, `set`, `perm`, `comment add|reply|resolve|list`, `rename`,
  `delete`, `save`, `upload`, `commit`, `export`, and `auth create|revoke`.
- `serve` for the HTTP server and `version` for the build version.

#### HTTP server and browser interface

- An HTMX 4 and Basecoat CSS interface, embedded in the binary, with login,
  a page detail view (rendered Markdown, properties, backlinks, discussion
  sidebar), a fields-and-body editor, a raw source editor, comment forms,
  side-by-side conflict resolution, board, table, and list views, and file
  uploads.
- A page change stream over SSE that follows the session and its policy and
  tells an open page or editor once when its file changes on disk.
- Security defaults: binding to `127.0.0.1`, sessions bound to the credential
  that created them, same-origin and anti-forgery checks on every
  state-changing request, a Content Security Policy on every response,
  sandboxed attachment serving, bounded request bodies, and `--behind-proxy`
  for TLS-terminating reverse proxies.

#### Browser/WASM runtime

- `cmd/jikko-wasm` builds the Go core for the browser with a read-only API
  (`tree`, `readMany`, `mentions`, `view`, `render`, `check`, `identity`,
  `exportZIP`) bound to one Identity in single-user local mode.
- `browser/`: an OPFS storage adapter, an offline HTMX transport, isomorphic-git
  for local and remote Git operations, and a CORS-free GitHub transport over
  the Git Database API.

#### Project

- Continuous integration: build, WASM build and tests, browser bundle check,
  gofmt, `go vet`, and `go test -race`.
- Release pipeline: GoReleaser builds for Linux, macOS (amd64 and arm64), and
  Windows (amd64) with checksums, a Homebrew formula for
  `KakkoiDev/homebrew-tap`, and a GitHub Pages workflow for the landing page
  in `site/`.
- Documentation: README with a quickstart, `agent.md` for AI agents,
  `architecture-browser.md`, `ux-plan.md`, and `docs/RELEASING.md`.

[Unreleased]: https://github.com/KakkoiDev/jikko/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/KakkoiDev/jikko/releases/tag/v0.1.0
