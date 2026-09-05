# hello-crossplane-aws

A Crossplane Control Plane Project that gives PaaS tenants a single self-service
API for their AWS kit.

One `TenantEnvironment` resource provisions an S3 bucket with versioning, and
optionally a DynamoDB table and an ECR repository. Tenants describe *what* they
want in a dozen lines of YAML; an embedded Go composition function decides *how*
it is built.

> **Status: proof of concept.** No IAM, no connection secrets, no production
> hardening. See [Not in scope](#not-in-scope).

## Why a control plane and not Terraform modules

The XRD published by this project becomes a real Kubernetes API endpoint:

```
POST /apis/platform.hello-crossplane.io/v1alpha1/namespaces/acme-dev/tenantenvironments
```

That gets you an OpenAPI-validated schema, RBAC, versioning, readable status
conditions, and continuous reconciliation — a controller keeps AWS matching the
spec and corrects manual drift — without writing a controller.

## Architecture

```
XR (TenantEnvironment)          namespace: acme-dev
        │
        ▼
Composition ── pipeline ──▶ compose-tenant-environment  (embedded Go function)
                          ──▶ function-auto-ready
        │
        ▼
Managed Resources               namespace: acme-dev
  Bucket ─ BucketVersioning ─ Table? ─ Repository?
        │
        ▼  provider reconcile loop (~1 min)
AWS: S3 · DynamoDB · ECR
```

Everything is namespaced. Crossplane v2 namespaced XRs compose namespaced managed
resources (the `.m.` API groups), so a tenant's resources are isolated by
namespace and reachable with ordinary RBAC.

## The API

```yaml
apiVersion: platform.hello-crossplane.io/v1alpha1
kind: TenantEnvironment
metadata:
  name: acme-dev
  namespace: acme-dev
spec:
  tenant: acme                  # immutable
  environment: dev              # immutable
  region: ap-southeast-1
  bucket:
    versioning: true
  table:
    enabled: true
    hashKey: id
    billingMode: PAY_PER_REQUEST
  repository:
    enabled: false
```

| Field                | Type   | Default           | Notes                                                |
| -------------------- | ------ | ----------------- | ---------------------------------------------------- |
| `tenant`             | string | required          | `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$`, **immutable** |
| `environment`        | enum   | required          | `dev` · `staging` · `prod`, **immutable**            |
| `region`             | enum   | `ap-southeast-1`  | Allow-list of supported regions                      |
| `bucket.versioning`  | bool   | `true`            | The S3 bucket is always created                      |
| `table.enabled`      | bool   | `false`           | Gates the DynamoDB table                             |
| `table.hashKey`      | string | `id`              | Partition key name                                   |
| `table.billingMode`  | enum   | `PAY_PER_REQUEST` | or `PROVISIONED`                                     |
| `repository.enabled` | bool   | `false`           | Gates the ECR repository                             |

Status exposes `bucketName`, `tableName` and `repositoryUrl` once the composed
resources report ready.

The `tenant` pattern is deliberately stricter than any one AWS service — it is
the intersection of the S3, DynamoDB and ECR naming rules. A name that passes the
schema is valid for all three, so you never provision two resources and fail on
the third.

### Naming

| Resource       | AWS name                |
| -------------- | ----------------------- |
| S3 bucket      | `<tenant>-<env>-bucket` |
| DynamoDB table | `<tenant>-<env>-dtbl`   |
| ECR repository | `<tenant>-<env>-ecr`    |

Applied through the `crossplane.io/external-name` annotation, not
`metadata.name`. The in-cluster names are generated and nobody needs to read them.

> S3 bucket names are unique **globally**, not per account. `acme-dev-bucket` may
> already belong to someone else. If a bucket sticks at `Synced=False`, check the
> provider event for a 409 and pick a more distinctive tenant name.

## Prerequisites

- `crossplane` CLI v2.5 or later
- A Docker-compatible container runtime — the CLI builds the function and runs a
  KIND cluster
- Go (latest stable)
- AWS credentials with S3, DynamoDB and ECR permissions

You do **not** need an existing Kubernetes cluster. `crossplane project run`
creates one.

## Quickstart

**1. Give the providers credentials**

```bash
kubectl create secret generic aws-secret -n crossplane-system \
  --from-file=creds=./aws-credentials.txt
```

`cluster/providerconfig.yaml` points at that secret. It is a
`ClusterProviderConfig` (`aws.m.upbound.io/v1beta1`) named `default`, which is
what managed resources fall back to when they don't set `providerConfigRef`.

**2. Run the project**

```bash
crossplane project run --extra-resources=cluster/providerconfig.yaml
```

This builds the Go function, builds a Configuration containing the XRD and
Composition, spins up a local control plane in KIND with its own OCI registry,
installs everything, and repoints your kubectl context. The first run is slow;
later runs reuse the cluster.

**3. Provision a tenant**

```bash
kubectl create namespace acme-dev
kubectl apply -f examples/tenantenvironments/acme-dev.yaml
```

**4. Watch it**

```bash
crossplane resource trace tenantenvironment acme-dev -n acme-dev
```

```
NAME                                    SYNCED  READY  STATUS
TenantEnvironment/acme-dev              True    True
├─ Bucket/acme-dev-bucket-x7k2p         True    True   Available
├─ BucketVersioning/acme-dev-ver-m3n9q  True    True   Available
└─ Table/acme-dev-dtbl-p2w8r            True    True   Available
```

**5. Tear down**

```bash
kubectl delete -f examples/tenantenvironments/acme-dev.yaml
crossplane project stop
```

## Development

The inner loop needs no cluster and no AWS account:

```bash
crossplane composition render \
  examples/tenantenvironments/acme-dev.yaml \
  apis/tenantenvironments/composition.yaml
```

Inside a project, `render` discovers the embedded function and builds it for you
— no functions file argument. It prints the managed resources the composition
would create. Run it after every change; it takes seconds versus minutes for a
real apply.

Go checks:

```bash
cd functions/compose-tenant-environment
gofmt -l . && go vet ./... && go test ./...
```

### Typed models

`crossplane dependency add` caches each provider package and generates Go
bindings for its CRDs under the `dev.crossplane.io/models` module. The function
builds a typed `Bucket` rather than assembling unstructured maps, so the compiler
catches field mistakes.

```bash
crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-s3
crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-dynamodb
crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-ecr
```

Never edit the generated models. To pick up new provider versions, run
`crossplane dependency update-cache`.

### Regenerating is destructive

`apis/tenantenvironments/definition.yaml` was generated once from `schema.yaml`
and is now maintained by hand. SimpleSchema cannot express CEL
`x-kubernetes-validations`, so re-running `crossplane xrd generate` **deletes the
tenant/environment immutability rules and the tenant name pattern**. Edit the XRD
directly.

The same applies to `crossplane composition generate`, which rewrites the
pipeline.

## Build and publish

```bash
crossplane project build --repository=ghcr.io/hoangviet1vu/hello-crossplane-aws
crossplane project push  --repository=ghcr.io/hoangviet1vu/hello-crossplane-aws --tag=v0.1.0
```

`build` produces the Configuration package and the embedded function package;
`push` ships both. Do not push the function separately.

Two things to get right:

- **Pass the same `--repository` to both commands.** It determines how embedded
  functions are referenced in the Composition. A mismatch produces a Composition
  pointing at an image that does not exist. Setting `spec.repository` in
  `crossplane-project.yaml` and omitting the flag also works.
- **Tags must be semantic versions, and packages are cached by tag.** Always bump
  rather than overwrite.

`push` reuses your Docker credentials, so `docker login ghcr.io` first. Installing
on a real cluster afterwards:

```bash
crossplane xpkg install configuration \
  ghcr.io/hoangviet1vu/hello-crossplane-aws:v0.1.0 --wait=5m
```

The package manager pulls the providers and the embedded function automatically —
they are dependencies of the Configuration.

## Versioning and CI

Package tags must be **semantic versions** — Crossplane's package manager rejects
anything else. A bare commit SHA is not one, so main builds use the SHA as a
semver prerelease. This is the same convention Crossplane's own function
templates use.

| Trigger                     | Version pushed        | Example                |
| --------------------------- | --------------------- | ---------------------- |
| Merge to `main`             | `v0.0.0-<short-sha>`  | `v0.0.0-a1b2c3d`       |
| Push a tag `v*`             | The tag name          | `v0.1.0`               |
| Pull request                | Nothing pushed        | —                      |

`v0.0.0-a1b2c3d` sorts below every real release, so a main build never wins a
version-constraint resolution against a tagged one. Tags stay authoritative,
main stays traceable to a commit.

Both are immutable once pushed, which matters because packages are cached by tag:
re-pushing an existing tag does not reliably update an installed package. Main
builds get a fresh version on every merge, so this never bites there.

The workflow lives in `.github/workflows/ci.yaml`:

- **Pull requests** run `gofmt`, `go vet`, `go test`, and `crossplane composition
  render` against every file in `examples/tenantenvironments/`. Nothing is pushed.
- **Merges and tags** run the same checks, then `crossplane project build` and
  `crossplane project push` with the computed version.

Four things the workflow has to get right:

- Pin the CLI with `XP_VERSION`. The project workflow is beta, so an unpinned
  `curl -sfL "https://cli.crossplane.io/install.sh" | sh` can change under you
  mid-sprint.
- Authenticate to GHCR with the built-in `GITHUB_TOKEN` and
  `permissions: packages: write`. `project push` reuses Docker credentials, so a
  `docker/login-action` step against `ghcr.io` is enough — no PAT needed.
- Populate the dependency cache before building. A fresh runner has none, so run
  `crossplane dependency update-cache` first and cache the directory between runs
  with `actions/cache` keyed on `crossplane-project.yaml`.
- Pass the same `--repository` to `build` and `push`, or set `spec.repository` in
  `crossplane-project.yaml` and omit the flag entirely. The second option is
  harder to get wrong.

Docker is required on the runner for both the function build and
`composition render`. `ubuntu-latest` has it.

## Troubleshooting

| Symptom                                            | Cause                                                                    |
| -------------------------------------------------- | ------------------------------------------------------------------------ |
| `no matches for kind "TenantEnvironment"`          | CRD not registered yet. `kubectl get xrd`, wait, retry.                   |
| MR is `Synced=False`, `cannot get ProviderConfig`  | Credentials step skipped, or the `ClusterProviderConfig` isn't `default`. |
| Function is healthy but no MRs appear              | The function returned no desired resources. Reproduce with `render`.      |
| `functionRef` points at a missing image            | `--repository` differed between `build` and `push`.                       |
| Bucket stuck with a 409                            | Global S3 name collision. See the note under [Naming](#naming).           |
| Package won't update                               | Same tag re-pushed. Bump the version.                                     |
| CI push rejected, invalid tag                      | The version isn't semver. See [Versioning and CI](#versioning-and-ci).    |
| CI build slow or fails resolving providers         | Dependency cache is cold. Run `crossplane dependency update-cache` first. |

Debug in three layers, in order: the XR (schema and CEL rules), the function
(`composition render`, then the function pod logs), and the managed resource
(`kubectl describe` for the provider's AWS error).

## Design decisions

**The project workflow, despite BETA.** `crossplane project run` collapses
build-push-install into one command, and `dependency add` generates the typed Go
models. For a PoC those are worth more than interface stability. The artifacts
are identical to what `crossplane xpkg build` produces, so falling back later is
mechanical.

**Namespace is `<tenant>-<env>`.** Simple and unambiguous. If tenants later grow
several environments that should share RBAC, namespace-per-tenant with the
environment as the XR name is a schema-compatible change — though it does mean
recreating XRs.

**One XRD, not three.** The three AWS resources share a lifecycle, a tenant and
an environment. Splitting them would create three objects to keep in sync for no
benefit. The `enabled` flags cover "some tenants need less".

**A Go function, not templates.** Naming rules, length limits and conditional
resources are logic. Logic belongs in code with table-driven tests.

**Terraform still owns the substrate.** Accounts, VPCs, the cluster Crossplane
runs in. Crossplane owns the per-tenant kit. That split is a stable end state,
not a migration phase.

## Not in scope

IAM roles and policies · connection secrets · KMS encryption · ArgoCD or GitOps
wiring · a tenant portal · multi-region · cost allocation tags · tenant
offboarding automation.

IAM is the first thing to add after the PoC: these three resources are not useful
until a tenant workload can reach them, and the alternative is tenants minting
their own IAM users.
