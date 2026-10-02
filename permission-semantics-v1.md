# Jikko v1 Permission Semantics

This document is normative and completes the permission semantics described in `identity-permissions.md`.

## Capability hierarchy

Permissions are hierarchical:

```text
admin -> write -> comment -> read
```

A grant of a capability includes every capability to its right. Thus `write` includes `comment` and `read`, and `admin` includes all four capabilities.

Grants obtained through multiple Identity memberships are additive. Jikko v1 has no deny rules.

## Capability meanings

- `read`: read and render the file.
- `comment`: read plus semantic comment operations: create, reply, and resolve comments.
- `write`: comment plus ordinary content and metadata mutations, including rename and deletion. `write` does not permit changing access-control policy.
- `admin`: write plus changing the file's `permissions` policy and authorization-sensitive administration.

`admin` is deliberately narrow. It is not required for ordinary rename or deletion.

## Open-by-default

An absent permission rule imposes no Jikko-level restriction. A file with no `permissions` mapping is unrestricted by Jikko. External filesystem, Git, repository, server, and operating-system controls still apply.

If a `permissions` mapping is present, each declared grant is evaluated through direct Identity match or transitive Identity membership, then expanded through the capability hierarchy above.

## Authentication bootstrap

A workspace MAY operate locally without authentication while no Jikko operation requires an authenticated Identity. Authentication becomes necessary when the harness must evaluate identity-specific permissions or attribute an authenticated operation.

The reference harness uses `jikko auth create <identity>` to create a credential for an existing individual Identity. The credential hash is stored in the gitignored `.auth.md`; the bearer token is displayed once.

Authentication MUST resolve to an individual Identity. An Identity containing `members` is a group and cannot directly authenticate.

The initial credential bootstrap is a harness-local administrative operation. A harness MUST NOT expose unauthenticated credential creation over an untrusted remote interface. Local bootstrap/recovery may rely on control of the workspace host as the external security boundary.

## Unusable policy

A `permissions` mapping that is present but cannot be evaluated MUST deny every
capability to every caller. An unreadable policy is not the absence of a policy
and MUST NOT be treated as open-by-default. This covers a `permissions` value
that is not a mapping, an empty mapping, and a mapping that could not be parsed
at all. The harness MUST report why.

An individual grant inside an otherwise readable mapping that cannot be
resolved -- an unknown or ambiguous Identity -- is skipped rather than fatal, so
it can only withhold access, never widen it. It MUST still be reported.

## ACL integrity

A Jikko harness MUST validate authorization references and reject or clearly report:

- permission grants referring to missing or ambiguous Identities;
- cyclic Identity membership;
- a proposed restricted ACL mutation that would leave no authenticated principal with effective `admin`, unless an explicit local recovery mechanism is being used.

Renaming an Identity through Jikko MUST update references to that Identity in permission policies as part of the same semantic operation. External filesystem renames are not guessed; `jikko check` reports resulting dangling references.

Deleting an Identity that is referenced by a permission policy MUST NOT silently weaken or broaden access. The semantic delete operation must either update/remove those references explicitly with authorization or reject the deletion and report the inbound ACL references.

## Authorization-sensitive Identity mutations

Group membership participates in authorization. Therefore a textual `write` grant on an Identity file is not sufficient authority to make every membership change.

Before accepting a membership mutation, the harness MUST evaluate whether the proposed graph change would change effective authorization. A caller MUST NOT be able to grant itself or another Identity capabilities that the caller is not authorized to administer merely by editing `members`.

This rule is transitive across nested Identity groups.

Membership is not the only field that carries authorization. Changing an Identity's `type`, or introducing a file whose name makes an existing Identity ambiguous, changes effective authorization just as much and MUST be evaluated the same way. The rule is about effect, not about which property was edited.

The practical form of the rule is a comparison. A harness computes the effective authorization of the workspace before and after the proposed change and rejects the change if any Identity would gain a capability on a file the caller is not authorized to administer. A change that only narrows access grants nothing to anyone and therefore requires no such authority.

## v1 exclusions

Jikko v1 deliberately has no explicit deny, folder ACL inheritance, role hierarchy, ACL inheritance, implicit permission propagation between files, or separate permission database. Portable authorization policy remains in Markdown frontmatter; reference-harness authentication credentials remain local in `.auth.md`.
