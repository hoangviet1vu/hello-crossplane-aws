# AGENTS.md

Instructions for AI coding agents working in this repository. Read this before
making any change.

The full guidance lives in the Kiro steering files under `.kiro/steering/`, which
are the single source of truth. Read all three before changing anything:

- **`.kiro/steering/product.md`** — what this repository is, the
  `TenantEnvironment` API, who it serves, and what is out of scope. This is a
  proof of concept: prefer the simplest thing that works end to end, and do not
  add features that were not asked for.
- **`.kiro/steering/tech.md`** — stack, Go conventions, the "regenerating is
  destructive" rules, commands, CI/versioning, and the gotchas that will bite you.
- **`.kiro/steering/design.md`** — repository layout, architecture, the hard
  invariants (API identity, namespacing, the `.m.` API groups, external-name
  naming, CEL immutability, optionality), the API schema, and the definition of
  done.

For end-user setup and background, see `README.md`.

Agents not running inside Kiro should read the three steering files directly —
they contain everything formerly duplicated here.

## Never commit build artifacts

Binary files generated during testing or verification (for example the
compiled embedded function produced by `go build` or
`crossplane composition render`) must be Git-ignored, never committed. The
compiled `compose-tenant-environment` function binary in particular shares its
module directory name and has no extension, so add an explicit `.gitignore`
entry when a new build output is not already covered.
