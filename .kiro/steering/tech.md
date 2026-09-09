---
inclusion: always
---

# Tech

## Stack

| Component     | Choice                                                                |
| ------------- | --------------------------------------------------------------------- |
| Control plane | Crossplane **v2.x** (v2 APIs only — no claims, no `X` prefix)          |
| Tooling       | `crossplane` CLI v2.5+, **project workflow** (`crossplane project …`)  |
| Composition   | Embedded composition function, **Go**, `function-sdk-go`              |
| Providers     | `crossplane-contrib/provider-aws-{s3,dynamodb,ecr}` **v2.x**          |
| Language      | Go (latest stable), `gofmt -s` formatted                             |
| CI/CD         | **GitHub Actions** — `.github/workflows/ci.yaml` (the only workflow)  |
| Registry      | GHCR — `…/hello-crossplane-aws` (tags), `…-dev` (main snapshots)       |

The project workflow is marked **[BETA]** in the CLI. That is an accepted risk
for this PoC. Do not rewrite it to raw `crossplane xpkg build` calls unless
explicitly asked — the artifacts are identical, so falling back later is
mechanical.

## Go conventions

- `gofmt -s` formatted. CI fails on any diff. `go vet ./...` must be clean.
- Keep `naming.go` (pure string-building and validation, **no Crossplane
  imports**) separate from `fn.go` (`RunFunction`), so the naming logic is
  trivially testable.
- Use the **generated models** from the `dev.crossplane.io/models` module rather
  than building unstructured objects by hand. `crossplane dependency add`
  generates them; typed access is the reason this project uses Go.
- Never hand-edit generated models. If a type is missing, add the dependency and
  regenerate.
- Follow the scaffold structure in `fn.go`: collect resources into a
  `map[resource.Name]any`, convert them to SDK types in the deferred block. Do
  not restructure into per-resource conversion.
- Wrap errors with `github.com/crossplane/crossplane-runtime/v2/pkg/errors`.
  Surface user-facing problems via `response.Fatal` / `response.Warning` so they
  land in the XR's events, not only the function pod log.
- Table-driven tests: `map[string]struct{...}` plus `t.Run(name, ...)`, compared
  with `github.com/google/go-cmp/cmp`.

## Regenerating is destructive

- `crossplane xrd generate --from simpleschema` does **not** produce CEL
  `x-kubernetes-validations`. Re-running it **deletes** the tenant/environment
  immutability rules and the tenant name pattern. `definition.yaml` is now
  hand-maintained — edit it directly.
- `crossplane composition generate` rewrites the pipeline. Once the files exist,
  edit them by hand.
- Never hand-edit generated Go models; add the dependency and regenerate instead.

## Commands

Run everything from the project root.

```bash
# Fast inner loop — no cluster, no AWS. render discovers and builds the
# function itself, so no functions file argument.
crossplane composition render \
  examples/tenantenvironments/acme-dev.yaml \
  apis/tenantenvironments/composition.yaml

# Go checks
cd functions/compose-tenant-environment && gofmt -l . && go vet ./... && go test ./...

# Local control plane (builds function + Configuration, repoints kubectl)
crossplane project run --extra-resources=cluster/providerconfig.yaml
crossplane project stop

# Dependencies (also regenerates the Go models)
crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-s3
crossplane dependency update-cache

# Publish — pass the SAME --repository to both, or set spec.repository once
crossplane project build --repository=ghcr.io/hoangviet1vu/hello-crossplane-aws
crossplane project push  --repository=ghcr.io/hoangviet1vu/hello-crossplane-aws --tag=v0.1.0

# Inspect a live XR
crossplane resource trace tenantenvironment acme-dev -n acme-dev
```

`project push` ships the Configuration **and** the embedded function packages.
Do not push the function separately.

## CI and versioning

`.github/workflows/ci.yaml` is the only workflow. Versioning **and the target
registry** rules are fixed:

| Trigger         | Registry pushed to                              | Version pushed       | Example          |
| --------------- | ----------------------------------------------- | -------------------- | ---------------- |
| Merge to `main` | `ghcr.io/hoangviet1vu/hello-crossplane-aws-dev` | `v0.0.0-<short-sha>` | `v0.0.0-a1b2c3d` |
| Tag `v*`        | `ghcr.io/hoangviet1vu/hello-crossplane-aws`     | The tag name         | `v0.1.0`         |
| Pull request    | — (nothing pushed)                              | —                    | —                |

Two registries, split by trigger. Main builds are unreleased snapshots and go to
the `-dev` repository so they never sit alongside real releases. Tagged builds
are the real thing and go to `ghcr.io/hoangviet1vu/hello-crossplane-aws`. The tag
flow is not wired up yet — the workflow only implements the main-merge path for
now, but the registry split is fixed so adding the tag job later is mechanical.

Because the registry now depends on the trigger, the workflow passes
`--repository` explicitly to **both** `build` and `push` per job. This overrides
`spec.repository` in `crossplane-project.yaml` (which stays pointed at the
release repo as the default for local publishing).

Package tags **must be semantic versions** — the package manager rejects a bare
commit SHA. `v0.0.0-<short-sha>` is a valid semver prerelease that carries the
hash and sorts below every real release, so a main build never wins a
version-constraint resolution against a tagged one.

Load-bearing workflow requirements:

- Pin the CLI with `XP_VERSION`. The project workflow is beta; an unpinned
  install can change under CI.
- `permissions: packages: write`; GHCR login via `docker/login-action` with the
  built-in `GITHUB_TOKEN`. `project push` reuses Docker credentials — no PAT.
- Run `crossplane dependency update-cache` before `project build`. Cache the
  directory with `actions/cache` keyed on `crossplane-project.yaml`.
- Pass `--repository` explicitly to both `build` and `push` — the target
  registry is trigger-dependent (`-dev` for main, the release repo for tags), so
  the workflow can no longer lean on `spec.repository`. Keep `spec.repository`
  set to the release repo as the local-publish default.
- Docker must be available for the function build and `composition render`.
- PR jobs run `crossplane dependency update-cache` (needed before `go vet`/
  `go test`, which resolve the generated `dev.crossplane.io/models` module
  under `schemas/go` via a `go.mod` replace directive), then `gofmt`, `go vet`,
  `go test`, and `composition render` over every example. They must never
  push.

## Things that will bite you

- `--repository` must match between `build` and `push`. It determines how
  embedded functions are referenced in the Composition.
- The `functionRef.name` is the project name and function name concatenated
  (`hello-crossplane-awscompose-tenant-environment`). It looks like a typo. It is
  not. The CLI generates it. Leave it alone.
- `ProviderConfig` is bootstrap, not package content. It must never end up inside
  the built xpkg — that is what `cluster/` is for.
- Packages are cached by tag. Re-pushing the same tag does not reliably update an
  installed package. Always bump.
- S3 bucket names are globally unique across all AWS accounts. A bucket stuck at
  `Synced=False` with a 409 is a name collision, not a bug in the function.
- Do not swap the Go function for `function-go-templating` or
  `function-patch-and-transform`. The typed Go function is the point.
