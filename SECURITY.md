# Security

Jikko is pre-release software. Do not rely on it as the only control protecting
sensitive material.

## Reporting a vulnerability

Report suspected vulnerabilities through GitHub's private vulnerability
reporting on this repository rather than in a public issue. Please include the
workspace shape needed to reproduce the behaviour.

## What Jikko authorization does and does not cover

Jikko's `permissions` policy governs operations performed **through a Jikko
harness**: the CLI, the HTTP server, and the Go core. It is not a sandbox.

- Anyone with direct filesystem, Git, repository-host, or operating-system
  write access to a workspace can read and change any file in it regardless of
  its `permissions` mapping. Those are separate security boundaries and Jikko
  does not attempt to replace them.
- Open-by-default is a Jikko semantic rule. A file without a `permissions`
  mapping is unrestricted *by Jikko*; whether it is private depends entirely on
  the surrounding system.
- A `permissions` mapping that is present but cannot be evaluated denies
  everyone. An unreadable policy is never treated as the absence of a policy.
  `jikko check` reports why.

## Credentials

`.auth.md` holds SHA-256 hashes of bearer tokens, never the tokens themselves.
It is harness-local state: it is gitignored, is not portable workspace source,
and is not disposable `.data`.

Adding `.auth.md` to `.gitignore` does not remove hashes already committed.
`jikko auth create` warns when the file is tracked by Git; if you see that
warning, untrack the file and rotate every token.

Credential creation is a host-local administrative operation. Do not expose it
over an untrusted remote interface: control of the workspace host is the
security boundary for bootstrap and recovery.

## Serving over a network

`jikko serve` binds to `127.0.0.1` by default and has no transport security of
its own. To reach it from elsewhere, put it behind a reverse proxy that
terminates TLS and pass `--behind-proxy` so session cookies are marked
`Secure`. Only pass that flag when a proxy you trust actually sets
`X-Forwarded-Proto`.

A browser session is bound to the credential it was exchanged for. Revoking
that credential, or deleting the Identity behind it or turning it into a
group, ends the session on its next request; an open event stream notices
within seconds and closes. Event streams never extend a session's expiry.

Every response from `jikko serve` carries a Content Security Policy that
allows only the server's own scripts, styles, and connections and forbids
framing (`frame-ancestors 'none'`), together with `X-Content-Type-Options:
nosniff`. The pages it serves need no inline script or style.

## The browser interface

Reading needs only the `read` capability a page's policy grants; editing,
commenting, and the edit forms themselves are offered only to an
authenticated caller with the capability, and every change goes through the
same staged, authorized core operations as the CLI.

Every state-changing request (`/login`, `/logout`, `/save/...`,
`/comment/...`) must be same-origin by Fetch Metadata or `Origin`, and must
carry an anti-forgery token: an HMAC, under a per-process key, of the
session id or, before login, of a random pre-session cookie. Both cookies
are `HttpOnly` and `SameSite=Strict`. A request authenticated with an
`Authorization: Bearer` header carries no ambient credential and needs no
token.

Page Markdown is rendered by Jikko's own renderer, which escapes every
character of source and emits only the elements it writes: raw HTML in a
page is displayed as text, links accept only `http`, `https`, `mailto`, and
same-site targets, and references or embeds of pages the reader may not
read render as if missing.

Workspace files (images, PDFs, and other attachments) carry no policy of
their own. `/files/...` serves a file only to a reader of a page that links,
embeds, or cites it, never a dotfile, harness state, or a symbolic link, and
always with `Content-Security-Policy: default-src 'none'; sandbox`; types a
browser would not display inline are sent as attachments.
