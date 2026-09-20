# Identity, Authentication, and Permissions Implementation

The first implementation follows `identity-permissions.md` and `permission-semantics-v1.md`.

Implemented in the Go core:

- `Identity` pages and nested membership;
- cycle and dangling-member validation;
- transitive membership;
- hierarchical `read`, `comment`, `write`, `admin` authorization;
- open-by-default files without a `permissions` mapping;
- dangling permission validation;
- local `.auth.md` bearer-token hashes;
- cryptographically random token generation and revocation;
- individual-only authentication;
- conservative protection for authorization-bearing group membership changes;
- fail-closed handling of an access policy that is present but cannot be evaluated;
- effect-based authorization for mutations;
- optimistic concurrency, atomic writes, and symlink containment for mutations;
- surgical frontmatter edits that preserve key order, comments, and YAML types.

`CanChangeMembers` remains the conservative pre-check it always was: changing a group that participates in ACLs requires effective `admin` on every affected file.

Mutations are no longer gated on that rule alone, because the specification asks for something more precise. Every mutation is evaluated by comparing the effective authorization of the workspace before and after the proposed change, and is rejected if it:

- introduces a workspace problem that did not exist before;
- raises any identity's effective capability on a file the caller may not administer;
- removes a restricted file's access policy without admin on that file;
- leaves a restricted file with no identity able to administer it.

This is simultaneously stricter and less restrictive than the original guard. Stricter, because it catches authorization changes that do not touch `members` at all — demoting an ACL-bearing Identity with `type`, for instance, used to pass as an ordinary write and silently stripped every grant naming it. Less restrictive, because removing a member widens nobody's access and no longer requires admin on every affected file.


## Work-model enforcement direction

The next runtime layer must enforce the documented work model without adding page types:

- deterministic validation available to CLI/JSON and browser;
- structured Task mutations for status, responsibility, dependencies, blockers, and progress;
- semantic comment/reply/resolve operations for durable discussion;
- proof references through ordinary links, embeds, commits, tests, measurements, files, external references, or approval;
- warnings or rejection for completion when configured requirements remain unmet;
- actor-attributed logical mutations and Git-backed audit history;
- onboarding and agent guidance that make Jikko the authoritative workspace.

Claim, checkpoint, evidence, decision, and event remain runtime concepts or conventions over Documents, Tasks, comments, references, and history. They do not become additional primitives.
