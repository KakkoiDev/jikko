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
