# Identity and Permissions

This document is normative for the Jikko v1 identity, authentication boundary, and authorization model. It supersedes the older `Group` and namespaced-identity design in `specification-v1.md` until that specification is consolidated.

## 1. Identity is the primitive

Jikko has one addressable actor primitive: `Identity`.

```text
no type        -> Document
type: task     -> Task
type: view     -> View
type: identity -> Identity
```

There are no separate Human, Agent, Group, Team, Person, or Role primitives.

An individual identity is simply an Identity without `members`:

```md
---
type: identity
---

# Alice
```

An identity with `members` is a group of identities:

```md
---
type: identity
members:
  - alice
  - ben
---

# Humans
```

The same mechanism represents any useful grouping:

```md
---
type: identity
members:
  - humans
  - agents
---

# Everyone
```

```md
---
type: identity
members:
  - alice
---

# Admins
```

`human`, `agent`, `admin`, `engineering`, and similar classifications are therefore expressed through membership rather than an identity `kind` or a separate role system.

Membership is transitive. If Alice is a member of `humans` and `humans` is a member of `everyone`, Alice is a member of `everyone`. Harnesses MUST detect membership cycles and MUST NOT recurse indefinitely.

Reverse membership is derived. Do not duplicate `groups:` onto each member merely for indexing.

## 2. Addressing

Identities use the same readable-name/path resolution philosophy as Jikko links. Source mentions therefore address an Identity directly:

```md
@alice
@codex
@admins
```

If a name is ambiguous, a path can disambiguate it, for example `@people/alice`. Folders remain organizational and carry no identity semantics.

A mention means attention. Assignment means responsibility:

```text
@alice          = attention
assignee: alice = responsibility
```

A group mention remains a reference to the group Identity. It MUST NOT be rewritten into the current member list; membership may change while authored intent must remain stable.

## 3. Identity and authentication

An Identity file states who/what exists in the workspace. It does not prove that the current caller owns that identity.

Authentication is a harness concern. A browser session, CLI environment, API credential, operating-system identity, OAuth provider, SSH key, or another trusted mechanism may authenticate a caller and map it to exactly one Jikko Identity.

Portable Jikko Markdown standardizes identities and authorization, not authentication technology. Harnesses MAY choose their own authentication mechanism.

Credentials, passwords, bearer tokens, private keys, and similar secrets MUST NOT be stored in portable Identity Markdown files merely to implement Jikko authentication. This is especially important for Git-backed workspaces because removing a committed secret from the current file does not remove it from repository history.

### 3.1 Reference harness authentication

The reference Go harness SHOULD initially implement a deliberately small token-based authentication mechanism.

A local `.auth.md` file at the workspace root maps credential hashes to individual Jikko Identities. `.auth.md` is harness-local configuration, not a Jikko Document and not portable workspace source. It MUST be excluded from normal workspace indexing and MUST be gitignored.

Conceptual shape:

```md
---
credentials:
  - identity: alice
    token_hash: "..."
  - identity: codex
    token_hash: "..."
---
```

The reference harness MUST store only a cryptographic hash of each bearer token, never the bearer token itself. Tokens MUST be generated using a cryptographically secure random source and contain sufficient entropy to resist guessing.

A conceptual CLI flow is:

```sh
jikko auth create alice
```

The harness generates a token, stores its hash in `.auth.md`, and displays the token once. The caller presents that token to authenticate as `alice`.

Multiple credentials MAY map to the same individual Identity so that credentials can be independently created and revoked without changing the Identity file.

For browser use, the permanent bearer token SHOULD be exchanged for a normal secure session rather than retransmitted unnecessarily on every request. Browser session cookies SHOULD use appropriate `HttpOnly`, `Secure`, and same-site protections when applicable.

The harness MUST refuse to treat an Identity with `members` as the direct authenticated caller. Authentication resolves to an individual Identity; group membership and permissions are then derived from the identity graph.

`.auth.md` contains security-sensitive authentication state even though it stores hashes rather than plaintext bearer tokens. It MUST NOT be treated as disposable `.data`, and deleting `.data` MUST NOT affect authentication credentials.

The reference harness SHOULD ensure `.auth.md` is present in `.gitignore`. If `.auth.md` is already tracked by Git, it SHOULD warn prominently and avoid implying that merely adding it to `.gitignore` removes prior credential hashes from Git history.

Cloning a Jikko repository therefore copies identities and permissions but intentionally does not copy reference-harness credentials. Authentication must be established locally on the new harness.

Jikko authorization governs operations performed through a Jikko harness. Direct filesystem, Git, repository-host, and operating-system access are separate security boundaries. An actor with unrestricted direct filesystem write access can bypass Jikko-level authorization.

## 4. File permissions

Any Jikko Markdown file MAY contain a `permissions` mapping in frontmatter.

The v1 permission vocabulary is deliberately small:

```text
read     read/render the file
comment  create/reply to/resolve comments without general content editing
write    modify ordinary content and metadata
admin    change access-control policy and perform access-sensitive administration
```

Example:

```yaml
permissions:
  read: customers
  comment: humans
  write: engineering
  admin: admins
```

A permission value may name one Identity or a list of Identities:

```yaml
permissions:
  write:
    - alice
    - engineering
```

Authorization includes transitive group membership. If `alice` is a member of `engineering`, a rule granting `write` to `engineering` grants Alice that capability.

An absent permission rule imposes no Jikko-level restriction. In particular, a file with no `permissions` mapping is unrestricted by Jikko. This does not override filesystem, Git, server, repository, or other external access controls.

Jikko v1 has no deny rules, folder permission inheritance, role hierarchy, permission inheritance, or implicit ACL propagation. Add those only if demonstrated workflows require them.

## 5. Permission semantics

`read` permits reading/rendering the current file.

`comment` is separate from `write` even though the current inline-comment design physically changes Markdown. A commenter may mutate comment markup/thread state through semantic comment operations without receiving arbitrary document-edit rights.

`write` permits ordinary document/body/frontmatter mutations but MUST NOT by itself permit changing the file's `permissions` policy.

`admin` permits changing access-control policy for the file and other explicitly administrative operations defined by the harness. The exact set of non-ACL administrative operations should remain small and explicit.

When a caller has access through multiple identities/groups, capabilities are additive: satisfying any applicable grant is sufficient. Jikko v1 has no explicit deny rule, so there is no deny/allow precedence problem.

## 6. Protecting authorization-bearing identities

Identity membership can itself affect authorization. For example, adding an Identity to `admins` may grant access to files whose `admin` permission names `admins`.

A Jikko harness MUST treat mutations that would change effective authorization as authorization-sensitive operations. An actor MUST NOT be able to gain a capability merely by editing an authorization-bearing Identity file through an operation for which it lacks the required authority.

This applies transitively: changing membership of a nested group can change effective authorization and must be evaluated accordingly.

The authorization check MUST evaluate the proposed semantic mutation, not merely whether the caller can textually write the Markdown file.

Harnesses SHOULD reject ACL mutations that would accidentally leave no authenticated principal capable of administering a restricted file, unless an explicit recovery/admin mechanism is being used.

## 7. Open-by-default model

A new Jikko workspace can begin with no ACLs at all. Adding a `permissions` rule introduces Jikko-level restriction for that file.

This preserves Jikko's plain-Markdown property: authorization metadata is optional rather than required boilerplate.

Open-by-default is a Jikko semantic rule, not a claim that a workspace is publicly accessible. A private local directory or private Git repository remains private according to its surrounding system.

## 8. Derived identity graph

The harness derives an identity membership graph from Identity files:

```text
Alice ----> Humans ----> Everyone
  |
  +-------> Engineering
  |
  +-------> Admins

Codex ----> Agents -----> Everyone
  |
  +-------> Engineering
```

The same graph supports:

- `@mention` resolution;
- Task assignment;
- group addressing;
- permission evaluation;
- derived membership/reverse-membership views;
- notification fan-out without rewriting source.

This is why Identity replaces Group as the primitive: one mechanism handles individual actors and arbitrary groups without separate Human, Agent, Group, Team, or Role types.

## 9. Design constraints

Keep the model small:

- no `kind: human|agent|group` unless a real semantic requirement appears;
- no separate Role primitive;
- no implicit `everyone` identity is required for open access;
- no authentication secrets in portable Identity Markdown;
- `.auth.md` is reference-harness-local, gitignored security state rather than portable Jikko source or disposable `.data`;
- no duplicated reverse membership;
- no permission inheritance in v1;
- no deny rules in v1;
- no assumption that Jikko ACLs sandbox direct filesystem access.

The intended core is:

```text
FILE
├── semantics
│   ├── Document
│   ├── Task
│   ├── View
│   └── Identity
├── relationships
│   ├── [[link]]
│   ├── ![[embed]]
│   ├── members
│   └── @mention
└── access
    └── permissions: read | comment | write | admin

REFERENCE HARNESS AUTH
└── .auth.md (gitignored)
    └── token hash -> individual Identity
```
