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
