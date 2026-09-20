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
- conservative protection for authorization-bearing group membership changes.

The current mutation guard is intentionally conservative: changing a group that participates in ACLs requires effective `admin` on every affected file. It can be relaxed later only if a demonstrated workflow requires finer-grained administration.


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
