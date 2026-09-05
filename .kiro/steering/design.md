---
inclusion: always
---

# Design & Structure

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

## Architecture

```
XR (TenantEnvironment)          namespace: <tenant>-<env>
        │
        ▼
Composition ── pipeline ──▶ compose-tenant-environment  (embedded Go function)
                          ──▶ function-auto-ready
        │
        ▼
Managed Resources               namespace: <tenant>-<env>
  Bucket ─ BucketVersioning ─ Table? ─ Repository?
        │
        ▼  provider reconcile loop (~1 min)
AWS: S3 · DynamoDB · ECR
```

Everything is namespaced. Crossplane v2 namespaced XRs compose namespaced managed
resources (the `.m.` API groups), isolating a tenant's resources by namespace.

## Invariants — do not change without being asked

### API identity

- Group: `platform.hello-crossplane.io`
- Kind: `TenantEnvironment`, plural `tenantenvironments`, version `v1alpha1`
- XRD is `apiextensions.crossplane.io/v2` with `spec.scope: Namespaced`
- No claims. Crossplane v2 namespaced XRs replace them.

### Namespacing

- One namespace per tenant-environment, named `<tenant>-<env>` (e.g. `acme-dev`).
- The XR lives in that namespace and is named `<tenant>-<env>`.
- All composed managed resources are namespaced and land in the same namespace.

### Managed resources must use the namespaced (`.m.`) API groups

| Resource       | apiVersion                          | Kind                    |
| -------------- | ----------------------------------- | ----------------------- |
| S3 bucket      | `s3.aws.m.upbound.io/v1beta1`       | `Bucket`                |
| S3 versioning  | `s3.aws.m.upbound.io/v1beta1`       | `BucketVersioning`      |
| DynamoDB       | `dynamodb.aws.m.upbound.io/v1beta1` | `Table`                 |
| ECR            | `ecr.aws.m.upbound.io/v1beta1`      | `Repository`            |
| ProviderConfig | `aws.m.upbound.io/v1beta1`          | `ClusterProviderConfig` |

The legacy cluster-scoped groups (`s3.aws.upbound.io/v1beta2` and friends) are
**wrong for this repo**. A namespaced XR cannot compose cluster-scoped resources.
Provider v2 ships both, so the wrong one compiles fine and fails at apply time.

### AWS resource naming

Set via the `crossplane.io/external-name` annotation on each managed resource,
**never** via `metadata.name`:

| Resource | External name           |
| -------- | ----------------------- |
| Bucket   | `<tenant>-<env>-bucket` |
| Table    | `<tenant>-<env>-dtbl`   |
| ECR repo | `<tenant>-<env>-ecr`    |

Composition-resource names (the keys in the desired-resources map) are stable
identifiers: `bucket`, `bucket-versioning`, `table`, `repository`. Never rename
them — renaming orphans live AWS resources.

### Immutability

`spec.tenant` and `spec.environment` carry CEL transition rules in
`definition.yaml` and must keep them:

```yaml
x-kubernetes-validations:
  - rule: "self == oldSelf"
    message: "tenant is immutable"
```

### Optionality

`spec.table.enabled` and `spec.repository.enabled` (both default `false`) gate the
DynamoDB table and ECR repository. When `false`, the function must **not** put
that key in the desired map at all. S3 is always created, always with versioning
`Enabled`.

## The API schema

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

Status exposes `bucketName`, `tableName`, and `repositoryUrl` once composed
resources report ready.

The `tenant` pattern is the intersection of the S3, DynamoDB, and ECR naming
rules, so a name that passes the schema is valid for all three. Rejecting at the
API server beats provisioning two resources and failing on the third.

## The XRD is hand-maintained after first generation

`definition.yaml` was generated once from `schema.yaml` and is now the source of
truth. SimpleSchema cannot express CEL validations, so re-running
`crossplane xrd generate` silently deletes the CEL rules. Edit `definition.yaml`
directly. `schema.yaml` is kept as documentation of the original shape; if you
change one, change both.

## Key design decisions

- **One XRD, not three.** The three AWS resources share a lifecycle, tenant, and
  environment. The `enabled` flags cover "some tenants need less".
- **A Go function, not templates.** Naming rules, length limits, and conditional
  resources are logic, and logic belongs in code with table-driven tests.
- **Namespace is `<tenant>-<env>`.** Simple and unambiguous.
- **Terraform owns the substrate; Crossplane owns the per-tenant kit.** A stable
  end state, not a migration phase.

## Definition of done

1. `gofmt -l` empty, `go vet ./...` and `go test ./...` pass.
2. `crossplane composition render` succeeds for **every** file in
   `examples/tenantenvironments/` — including the table/repository-disabled
   variants.
3. Every composed resource carries the correct `crossplane.io/external-name`.
4. If `definition.yaml` changed: the CEL rules survived, and `examples/` plus the
   API table in `README.md` were updated in the same commit.
5. Composition-resource names are unchanged, or the change is called out in the
   PR description as breaking.
