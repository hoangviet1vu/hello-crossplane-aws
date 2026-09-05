# AGENTS.md

Instructions for AI coding agents working in this repository. Read this before
making any change.

## What this repository is

`hello-crossplane-aws` is a **Crossplane Control Plane Project** that publishes a
single self-service API, `TenantEnvironment`, for a multi-tenant PaaS. One
`TenantEnvironment` provisions an S3 bucket with versioning (always), plus an
optional DynamoDB table and ECR repository, for one tenant in one environment.

This is a **proof of concept**. Prefer the simplest thing that works end to end
over completeness. Do not add features that were not asked for.

## Tech stack

| Component     | Choice                                                                |
| ------------- | --------------------------------------------------------------------- |
| Control plane | Crossplane **v2.x** (v2 APIs only — no claims, no `X` prefix)          |
| Tooling       | `crossplane` CLI v2.5+, **project workflow** (`crossplane project …`)  |
| Composition   | Embedded composition function, **Go**, `function-sdk-go`               |
| Providers     | `crossplane-contrib/provider-aws-{s3,dynamodb,ecr}` **v2.x**           |
| Language      | Go (latest stable), `gofmt` formatted                                  |
| Registry      | GitHub Packages — `ghcr.io/hoangviet1vu/hello-crossplane-aws`          |

The project workflow is marked **[BETA]** in the CLI. That is an accepted risk
for this PoC. Do not "stabilise" it by rewriting to raw `crossplane xpkg build`
calls unless explicitly asked.

## Repository layout

Scaffolded by `crossplane project init`. The CLI depends on this layout — do not
restructure it.

```
crossplane-project.yaml            Project metadata: name, repository, dependencies
apis/
  tenantenvironments/
    schema.yaml                    SimpleSchema source for the API
    definition.yaml                XRD — generated once, then hand-maintained
    composition.yaml               Composition — pipeline managed by the CLI
functions/
  compose-tenant-environment/      Embedded Go function
    fn.go  naming.go               Implementation
    fn_test.go  naming_test.go     Table-driven tests
examples/
  tenantenvironments/              Sample XRs; also the render inputs
tests/                             Composition tests
operations/                        Unused in this PoC
cluster/                           NOT part of the package — ProviderConfig etc.
_output/                           Build artifacts (gitignored)
```

`cluster/` is ours, not the CLI's. It holds things that must never end up inside
the built package: `ProviderConfig`, credential wiring, bootstrap manifests.

## Invariants — do not change these without being asked

**API identity**

- Group: `platform.hello-crossplane.io`
- Kind: `TenantEnvironment`, plural `tenantenvironments`, version `v1alpha1`
- XRD is `apiextensions.crossplane.io/v2` with `spec.scope: Namespaced`
- No claims. Crossplane v2 namespaced XRs replace them.

**Namespacing**

- One namespace per tenant-environment, named `<tenant>-<env>` (e.g. `acme-dev`).
- The XR lives in that namespace and is named `<tenant>-<env>`.
- All composed managed resources are namespaced and land in the same namespace.

**Managed resources must use the namespaced (`.m.`) API groups**

| Resource       | apiVersion                          | Kind                    |
| -------------- | ----------------------------------- | ----------------------- |
| S3 bucket      | `s3.aws.m.upbound.io/v1beta1`       | `Bucket`                |
| S3 versioning  | `s3.aws.m.upbound.io/v1beta1`       | `BucketVersioning`      |
| DynamoDB       | `dynamodb.aws.m.upbound.io/v1beta1` | `Table`                 |
| ECR            | `ecr.aws.m.upbound.io/v1beta1`      | `Repository`            |
| ProviderConfig | `aws.m.upbound.io/v1beta1`          | `ClusterProviderConfig` |

The legacy cluster-scoped groups (`s3.aws.upbound.io/v1beta2` and friends) are
**wrong for this repo**. A namespaced XR cannot compose cluster-scoped resources.
Provider v2 ships both, so picking the wrong one compiles fine and fails at
apply time.

**AWS resource naming** — set via the `crossplane.io/external-name` annotation on
each managed resource, never via `metadata.name`:

| Resource | External name           |
| -------- | ----------------------- |
| Bucket   | `<tenant>-<env>-bucket` |
| Table    | `<tenant>-<env>-dtbl`   |
| ECR repo | `<tenant>-<env>-ecr`    |

Composition-resource names (the keys in the desired-resources map) are stable
identifiers: `bucket`, `bucket-versioning`, `table`, `repository`. Never rename
them — renaming orphans live AWS resources.

**Immutability** — `spec.tenant` and `spec.environment` carry CEL transition
rules in `definition.yaml` and must keep them:

```yaml
x-kubernetes-validations:
  - rule: "self == oldSelf"
    message: "tenant is immutable"
```

**Optionality** — `spec.table.enabled` and `spec.repository.enabled` (both
default `false`) gate the DynamoDB table and ECR repository. When `false`, the
function must not put that key in the desired map at all. S3 is always created,
always with versioning `Enabled`.

**Out of scope** — do not add: IAM roles or policies, KMS, connection secrets,
ArgoCD manifests, a portal, multi-region, or tags beyond
`tenant`/`environment`/`managed-by`.

## The XRD is hand-maintained after first generation

`crossplane xrd generate --from simpleschema` produces types, defaults,
descriptions and numeric bounds. It does **not** produce CEL
`x-kubernetes-validations`, which this API depends on for tenant/environment
immutability and the tenant name pattern.

So `apis/tenantenvironments/definition.yaml` was generated once and is now the
source of truth. **Re-running `xrd generate` silently deletes the CEL rules.**
Edit `definition.yaml` directly. `schema.yaml` is kept as documentation of the
original shape; if you change one, change both.

The `tenant` pattern is the intersection of the S3, DynamoDB and ECR naming
rules, so a name that passes the schema is valid for all three. Rejecting at the
API server beats provisioning two resources and failing on the third.

## Go conventions

- `gofmt -s` formatted. CI fails on any diff. `go vet ./...` clean.
- `naming.go` holds pure string-building and validation with **no Crossplane
  imports**. `fn.go` holds `RunFunction`. Keep them separate so the naming logic
  is trivially testable.
- Use the **generated models** from the `dev.crossplane.io/models` module rather
  than building unstructured objects by hand. `crossplane dependency add`
  generates them, and that typed access is the reason this project uses Go.
- Never hand-edit generated models. If a type is missing, add the dependency and
  regenerate.
- Follow the scaffold's structure in `fn.go`: collect resources into a
  `map[resource.Name]any`, convert them to SDK types in the deferred block. Do
  not restructure this into per-resource conversion.
- Errors wrap with `github.com/crossplane/crossplane-runtime/v2/pkg/errors`.
  Surface user-facing problems via `response.Fatal` / `response.Warning` so they
  land in the XR's events, not only in the function pod log.
- Table-driven tests: `map[string]struct{...}` plus `t.Run(name, ...)`, compared
  with `github.com/google/go-cmp/cmp`.

## Commands

Run everything from the project root.

```bash
# Fast inner loop — no cluster, no AWS. In a project, render discovers and
# builds the function itself, so no functions file argument.
crossplane composition render \
  examples/tenantenvironments/acme-dev.yaml \
  apis/tenantenvironments/composition.yaml

# Go checks
cd functions/compose-tenant-environment && gofmt -l . && go vet ./... && go test ./...

# Local control plane: builds the function, builds and installs the
# Configuration, repoints kubectl. Reuses the KIND cluster on later runs.
crossplane project run --extra-resources=cluster/providerconfig.yaml
crossplane project stop

# Dependencies (also regenerates the Go models)
crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-s3
crossplane dependency update-cache

# Publish
crossplane project build --repository=ghcr.io/hoangviet1vu/hello-crossplane-aws
crossplane project push  --repository=ghcr.io/hoangviet1vu/hello-crossplane-aws --tag=v0.1.0

# Inspect a live XR
crossplane resource trace tenantenvironment acme-dev -n acme-dev
```

`project push` ships the Configuration **and** the embedded function packages.
Do not push the function separately.

## CI and versioning

`.github/workflows/ci.yaml` is the only workflow. Its versioning rules are fixed:

| Trigger         | Version pushed       | Example          |
| --------------- | -------------------- | ---------------- |
| Merge to `main` | `v0.0.0-<short-sha>` | `v0.0.0-a1b2c3d` |
| Tag `v*`        | The tag name         | `v0.1.0`         |
| Pull request    | Nothing pushed       | —                |

**Do not use a bare commit SHA as the tag.** Package tags must be semantic
versions; the package manager rejects anything else. `v0.0.0-<short-sha>` is a
valid semver prerelease that still carries the hash, and it sorts below every
real release so a main build never wins a version-constraint resolution against
a tagged one. This is the convention Crossplane's own function templates use.

Workflow requirements, all load-bearing:

- Pin the CLI with `XP_VERSION` when running
  `curl -sfL "https://cli.crossplane.io/install.sh" | sh`. The project workflow
  is beta and an unpinned install can change under CI.
- `permissions: packages: write`, GHCR login via `docker/login-action` with the
  built-in `GITHUB_TOKEN`. `project push` reuses Docker credentials — no PAT.
- Run `crossplane dependency update-cache` before `project build`. A fresh runner
  has an empty cache. Cache the directory with `actions/cache` keyed on
  `crossplane-project.yaml`.
- Prefer setting `spec.repository` in `crossplane-project.yaml` over passing
  `--repository` to both `build` and `push`. One source of truth, one less way to
  mismatch them.
- Docker must be available for the function build and for `composition render`.

PR jobs run `gofmt`, `go vet`, `go test`, and `composition render` over every
example. They must never push.

## Definition of done

1. `gofmt -l` empty, `go vet ./...` and `go test ./...` pass.
2. `crossplane composition render` succeeds for **every** file in
   `examples/tenantenvironments/` — including the variants with the table and
   repository disabled.
3. Every composed resource carries the correct `crossplane.io/external-name`.
4. If `definition.yaml` changed: the CEL rules survived, and `examples/` plus the
   API table in `README.md` were updated in the same commit.
5. Composition-resource names are unchanged, or the change is called out in the
   PR description as breaking.

## Things that will bite you

- **`--repository` must match between `build` and `push`.** It determines how
  embedded functions are referenced in the Composition. Mismatch it and the
  Composition points at a function image that does not exist.
- **The `functionRef.name` in the Composition is the project name and function
  name concatenated** (`hello-crossplane-awscompose-tenant-environment`). It
  looks like a typo. It is not. The CLI generates it. Leave it alone.
- **Re-running generators overwrites hand edits.** `xrd generate` drops CEL
  rules; `composition generate` rewrites the pipeline. Once the files exist,
  edit them by hand.
- **`ProviderConfig` is bootstrap, not package content.** It holds credentials
  and must never end up inside the built xpkg. That is what `cluster/` is for.
- **Packages are cached by tag.** Re-pushing the same tag does not reliably
  update an installed package. Always bump. Main builds are safe here because
  every merge produces a new `v0.0.0-<short-sha>`, but re-tagging a release is not.
- **S3 bucket names are globally unique across all AWS accounts.**
  `<tenant>-<env>-bucket` can collide with a stranger's bucket. A bucket stuck at
  `Synced=False` with a 409 is this, not a bug in the function.
- **Do not swap the Go function for `function-go-templating` or
  `function-patch-and-transform`.** The typed Go function is the point.
