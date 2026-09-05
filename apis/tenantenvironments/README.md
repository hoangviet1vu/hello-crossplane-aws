# `apis/tenantenvironments/`

This folder defines the **`TenantEnvironment`** API — the single self-service
API this project publishes. These files describe *what* a tenant can ask for; a
Crossplane composition (implemented elsewhere) decides *how* it is built.

## Files

| File              | Role                                                                 |
| ----------------- | -------------------------------------------------------------------- |
| `definition.yaml` | **Authoritative** XRD. The real Kubernetes API endpoint definition.  |
| `schema.yaml`     | Human-readable SimpleSchema mirror. Documentation only.              |
| `composition.yaml`| The composition pipeline. *(added by the composition/function slice)*|

### `definition.yaml` — the source of truth

This is the `CompositeResourceDefinition` (XRD), `apiextensions.crossplane.io/v2`,
`scope: Namespaced`. When installed it becomes a real Kubernetes API:
`TenantEnvironment` in group `platform.hello-crossplane.io`, version `v1alpha1`.
It gives OpenAPI-validated schema, RBAC, versioning, readable status conditions,
and continuous reconciliation — without writing a controller.

It carries things a schema generator cannot express and that you must not lose:

- the tenant name **`pattern`** `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$`, and
- the **CEL immutability rules** (`self == oldSelf`) on `spec.tenant` and
  `spec.environment`.

### `schema.yaml` — documentation, kept in sync by hand

This is the Crossplane **SimpleSchema** source the XRD was first generated from
(`crossplane xrd generate --from simpleschema`). It is easier to read than the
full OpenAPI form, so it is retained as documentation of the API shape. It is
**not** authoritative and is not installed into the cluster.

## Why two files instead of one

SimpleSchema is a convenient authoring format but **cannot express CEL
`x-kubernetes-validations`**. Generating the XRD from it and then adding the CEL
rules by hand gives the best of both — until someone regenerates.

## Regenerating is destructive — do NOT re-run `xrd generate`

Re-running `crossplane xrd generate` overwrites `definition.yaml` from
`schema.yaml` and **silently deletes** the tenant `pattern` and both immutability
rules, because SimpleSchema cannot represent them. `definition.yaml` is therefore
**hand-maintained**. Edit it directly.

If you change the API shape (a field, type, enum set, or default), make the
identical change in **both** files by hand. The only things that intentionally
live only in `definition.yaml` are the CEL rules and the tenant `pattern`.

## Keeping the two in agreement

A guard test in `tests/schema/` walks the union of `spec`/`status` fields and
fails if `definition.yaml` and `schema.yaml` disagree on name, nesting, type,
enum set, or default (excluding the CEL rules and pattern, which are
definition-only). Other guard tests assert the XRD identity, the presence of the
CEL rules with their exact messages, and the example shapes.

```bash
cd tests/schema && gofmt -s -l . && go vet ./... && go test ./...
```

## The API at a glance

| Field                | Type   | Default           | Notes                                             |
| -------------------- | ------ | ----------------- | ------------------------------------------------- |
| `tenant`             | string | required          | pattern above, **immutable**                      |
| `environment`        | enum   | required          | `dev` / `staging` / `prod`, **immutable**         |
| `region`             | enum   | `ap-southeast-1`  | allow-list of supported regions                   |
| `bucket.versioning`  | bool   | `true`            | the S3 bucket is always created                   |
| `table.enabled`      | bool   | `false`           | gates the DynamoDB table                          |
| `table.hashKey`      | string | `id`              | partition key name                                |
| `table.billingMode`  | enum   | `PAY_PER_REQUEST` | or `PROVISIONED`                                  |
| `repository.enabled` | bool   | `false`           | gates the ECR repository                          |

`status` exposes `bucketName`, `tableName`, and `repositoryUrl`, populated by the
composition once the composed resources report ready.
