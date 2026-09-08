# Design Document

## Overview

This slice delivers the first end-to-end vertical of `hello-crossplane-aws`:
applying a `TenantEnvironment` provisions a real AWS **S3 bucket** with a
versioning configuration. The `TenantEnvironment` API already exists as a
hand-maintained XRD (`apis/tenantenvironments/definition.yaml`). This design adds
the two remaining pieces the slice needs:

1. The **AWS S3 provider** (`provider-aws-s3`, v2.x) as a project dependency, with
   its typed Go models generated under `dev.crossplane.io/models`.
2. An **embedded Go composition function** (`compose-tenant-environment`) plus a
   **Composition** (`apis/tenantenvironments/composition.yaml`) that, for every
   `TenantEnvironment`, emits exactly two namespaced managed resources — a
   `Bucket` (always) and a `BucketVersioning` — and populates `status.bucketName`
   once the bucket reports ready.

Scope is **S3 only**. The `spec.table` and `spec.repository` fields already exist
in the API but are deliberately **not** composed in this slice. DynamoDB and ECR
are clean future slices. No IAM, KMS, connection secrets, GitOps, multi-region,
or tags beyond `tenant` / `environment` / `managed-by`.

Guiding principle: the simplest thing that works end to end for S3.

### How this satisfies the requirements

| Requirement | Where addressed |
| ----------- | --------------- |
| R1 — Add S3 provider dependency | Architecture → Dependency & models; already present under `schemas/go` |
| R2 — Provision the bucket for every XR | Components → `fn.go` flow, Bucket resource shape |
| R3 — Name via external-name | Components → `naming.go`, Bucket external-name annotation |
| R4 — Gate versioning on spec | Components → BucketVersioning resource shape, versioning mapping |
| R5 — Report `status.bucketName` on ready | Components → status population |
| R6 — Restrict slice to S3 | Components → exactly-two-resources invariant; no Table/Repository |
| R7 — Surface errors via response | Error Handling |
| R8 — Definition of done | Testing Strategy |

## Architecture

```
TenantEnvironment (XR)                 namespace: <tenant>-<env>
  apiVersion: platform.hello-crossplane.io/v1alpha1
  spec: { tenant, environment, region, bucket.versioning, table*, repository* }
        │
        ▼
Composition  apis/tenantenvironments/composition.yaml
  mode: Pipeline
  ├─ step: compose-tenant-environment   (embedded Go function — this slice)
  │     reads observed XR → derives names → emits desired MRs → writes status
  └─ step: function-auto-ready          (marks XR Ready when composed MRs are Ready)
        │
        ▼
Desired managed resources               namespace: <tenant>-<env>
  map key "bucket"             → Bucket             (s3.aws.m.upbound.io/v1beta1)
  map key "bucket-versioning"  → BucketVersioning   (s3.aws.m.upbound.io/v1beta1)
        │
        ▼  provider-aws-s3 reconcile loop (~1 min)
AWS: S3 bucket "<tenant>-<env>-bucket" + versioning Enabled|Suspended
```

Everything is namespaced. The XR is a Crossplane **v2 namespaced composite**
(`apiextensions.crossplane.io/v2`, `scope: Namespaced`, no claims). Namespaced XRs
compose only namespaced managed resources — the `.m.` API groups. The
cluster-scoped legacy group `s3.aws.upbound.io` compiles but fails at apply and is
never used here (R6.4).

### Dependency and generated models (R1)

`provider-aws-s3` is declared in `crossplane-project.yaml` and added via:

```bash
crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-s3
crossplane dependency update-cache
```

`dependency add` regenerates the typed Go models under the
`dev.crossplane.io/models` module (rooted at `schemas/go`). The namespaced S3
types the function needs already exist there:

- `dev.crossplane.io/models/io/upbound/m/aws/s3/v1beta1` — `Bucket`,
  `BucketVersioning`.

These models are **tool-generated and never hand-edited** (R1.3). If a needed type
is missing, the fix is to re-add the dependency and regenerate, not to edit the
files (R1.4). `crossplane-project.yaml` gains a single dependency entry pinned to
the `v2.x` range (`>=v0.0.0` as scaffolded resolves to the latest v2 provider;
the intent is v2.x per steering).

### The `.m.` group choice

| Resource | apiVersion | Kind | Map key |
| -------- | ---------- | ---- | ------- |
| S3 bucket | `s3.aws.m.upbound.io/v1beta1` | `Bucket` | `bucket` |
| S3 versioning | `s3.aws.m.upbound.io/v1beta1` | `BucketVersioning` | `bucket-versioning` |

Provider Kinds are the upstream defaults — `Bucket` and `BucketVersioning`, not
renamed. Map keys are stable identifiers; renaming them orphans live AWS
resources, so they are fixed by contract (R3.5, R6.3).

## Components and Interfaces

Two source files, split so the naming logic is testable without Crossplane
imports:

```
functions/compose-tenant-environment/
  fn.go          RunFunction — the composition entrypoint (imports Crossplane SDK)
  naming.go      pure string-building + validation (NO Crossplane imports)
  fn_test.go     table-driven tests for RunFunction
  naming_test.go table-driven tests for naming.go
  go.mod go.sum  module for the embedded function
```

### `apis/tenantenvironments/composition.yaml`

A `Composition` targeting the `TenantEnvironment` XR, `mode: Pipeline`, with two
steps:

```yaml
apiVersion: apiextensions.crossplane.io/v1
kind: Composition
metadata:
  name: tenantenvironments
spec:
  compositeTypeRef:
    apiVersion: platform.hello-crossplane.io/v1alpha1
    kind: TenantEnvironment
  mode: Pipeline
  pipeline:
    - step: compose-tenant-environment
      functionRef:
        # project name + function name, concatenated by the CLI. Not a typo.
        name: hello-crossplane-awscompose-tenant-environment
    - step: auto-ready
      functionRef:
        name: function-auto-ready
```

- `compose-tenant-environment` emits the desired resources and writes status.
- `function-auto-ready` (a standard Crossplane function) sets the XR `Ready`
  condition once every composed MR reports `Ready`. It is the mechanism behind
  R5's "when the Bucket reports ready".
- `composition.yaml` is written once and then hand-edited; do not re-run
  `crossplane composition generate`, which rewrites the pipeline.

### `naming.go` — pure naming and validation (R3, R2.7, R3.6)

No Crossplane imports (R8.5). Exports:

```go
package main

// Names holds the deterministic identifiers derived from a TenantEnvironment.
type Names struct {
    Namespace  string // "<tenant>-<env>"
    BucketName string // "<tenant>-<env>-bucket"  (the AWS external name)
}

// BuildNames validates tenant and environment and returns the derived names.
// It returns an error if either input is empty/whitespace, so the caller can
// refuse to emit resources with a malformed external name.
func BuildNames(tenant, environment string) (Names, error)
```

- `BucketName` is the literal concatenation `<tenant>-<env>-bucket` (R3.1).
- Deterministic: identical inputs always yield a byte-identical result — no time,
  randomness, or map iteration (R3.2).
- Empty or whitespace-only `tenant` or `environment` yields an error and no names
  (R3.6, R2.7). The XRD pattern already rejects malformed tenants at the API
  server; this is defense in depth so the function never emits a bucket with a
  broken external name.

`naming.go` also holds the small pure helpers for the versioning status mapping
and the tag set, keeping `fn.go` free of business rules:

```go
// VersioningStatus maps the spec flag to the provider enum value.
// true → "Enabled", false → "Suspended".
func VersioningStatus(enabled bool) string

// StandardTags returns the fixed tag set for every composed resource.
// Exactly {tenant, environment, managed-by}, no more.
func StandardTags(tenant, environment string) map[string]string
```

### `fn.go` — `RunFunction` (R2, R4, R5, R6, R7)

Follows the `function-sdk-go` scaffold structure. High-level flow:

1. **Read the observed XR.** Use `request.GetObservedCompositeResource(req)` and
   read `spec.tenant`, `spec.environment`, `spec.region`, and
   `spec.bucket.versioning` from it. If the XR cannot be read or a required field
   is missing/invalid, call `response.Fatal` and return without emitting any
   desired resources (R7.1).
2. **Derive names.** Call `BuildNames(tenant, environment)`. On error, wrap it
   (`errors.Wrap`) and `response.Fatal`; emit nothing (R2.7, R3.6, R7.1).
3. **Resolve `region`.** Read `spec.region`; the XRD default (`ap-southeast-1`)
   means it is populated on the observed XR, but the function also tolerates an
   empty value defensively.
4. **Resolve versioning.** Read `spec.bucket.versioning`; treat absent as `true`
   (schema default) → `VersioningStatus(true)` = `Enabled` (R4.5).
5. **Build desired resources into a map.** Collect into
   `map[resource.Name]any`, keyed exactly `"bucket"` and `"bucket-versioning"`
   (R2.1, R4.1, R6.3), using the typed generated models:
   - `bucket` → `s3v1beta1.Bucket`
   - `bucket-versioning` → `s3v1beta1.BucketVersioning`
6. **Convert in a deferred block.** After the map is assembled, convert each entry
   to the SDK's desired-resource representation and write it to
   `response`. This is the scaffold's deferred-conversion pattern — do not
   restructure into per-resource conversion (steering: Go conventions).
7. **Write status.** Merge composed-resource readiness (see status population
   below) and set `status.bucketName` when the bucket is ready (R5).
8. **Preserve/return.** Return `rsp` with the desired resources and any
   `response.Warning`/`response.Fatal` attached to the XR (R7.2).

The observed XR spec is read as typed data where practical; the SDK exposes the
XR as an unstructured composite, and this slice reads scalar spec fields from it.
The composed **managed resources** are always built from the generated models
(steering: "use the generated models … typed access is the reason this project
uses Go").

#### Bucket resource shape (R2, R3)

Built as `s3v1beta1.Bucket`:

- `apiVersion: s3.aws.m.upbound.io/v1beta1`, `kind: Bucket` (R2.3, R6.4) — carried
  by the model's typed `APIVersion`/`Kind` constants.
- `metadata.namespace: <tenant>-<env>` (R2.4). Emitted so the MR lands in the XR's
  namespace.
- `metadata.annotations["crossplane.io/external-name"] = <tenant>-<env>-bucket`
  (R2.6, R3.3). **`metadata.name` is never set to the external name** or any
  tenant/env-derived value (R2.6, R3.4); it is left to the composition machinery.
- `spec.forProvider.region = spec.region` (R2.5).
- `spec.forProvider.tags = {tenant, environment, managed-by}` (R6.5) via
  `StandardTags`.

#### BucketVersioning resource shape (R4)

Built as `s3v1beta1.BucketVersioning`:

- `apiVersion: s3.aws.m.upbound.io/v1beta1`, `kind: BucketVersioning` (R4.2, R6.4).
- `metadata.namespace: <tenant>-<env>` — same namespace as the bucket (R4.6).
- `spec.forProvider.versioningConfiguration.status = Enabled | Suspended`,
  mapped from `spec.bucket.versioning` via `VersioningStatus` (R4.3, R4.4, R4.5).
  No other status value is ever set.
- **Reference to the bucket** (R4.7): set `spec.forProvider.bucket` to the bucket
  external name (`<tenant>-<env>-bucket`). Because the bucket's AWS name is the
  external name, referencing by name is deterministic and independent of the
  composition-generated `metadata.name`. This ties the versioning resource to the
  same bucket the function emitted under map key `bucket`.
- If the `bucket` entry is absent from the desired map, the function does not emit
  `bucket-versioning` and surfaces an error (R4.8). In this slice the bucket is
  unconditional, so this guard only fires on an internal inconsistency.

#### Status population (R5)

`status.bucketName` reflects a *currently-ready* bucket:

- Read the **observed composed** `bucket` resource's readiness (its `Ready`
  condition, as reported back by the provider on a later reconcile).
- When the bucket is `Ready`, set `status.bucketName` to the bucket external name,
  byte-for-byte, no prefix/suffix/whitespace (R5.1, R5.4).
- While not ready, leave `status.bucketName` unset (R5.2).
- If it transitions ready → not-ready, clear `status.bucketName` (R5.3). This
  falls out naturally from deriving status from current observed readiness on
  every reconcile rather than latching it.
- `status.tableName` and `status.repositoryUrl` are left unset in this slice
  (R5.5, R5.6).

Because the value written equals the external name derived by `BuildNames`,
status is a pure function of readiness plus the deterministic name.

## Data Models

### TenantEnvironment (input) — already defined

The XRD (`definition.yaml`) is the source of truth and is unchanged by this slice.
Fields read by the function:

| Field | Type | Default | Used for |
| ----- | ---- | ------- | -------- |
| `spec.tenant` | string (immutable, pattern) | required | name derivation, tags |
| `spec.environment` | enum `dev`/`staging`/`prod` (immutable) | required | name derivation, tags |
| `spec.region` | enum allow-list | `ap-southeast-1` | `Bucket.forProvider.region` |
| `spec.bucket.versioning` | bool | `true` | versioning status mapping |

`spec.table.*` and `spec.repository.*` exist in the schema but are **ignored** by
this slice (R6.1, R6.2).

Status contract written by the function:

| Field | Type | This slice |
| ----- | ---- | ---------- |
| `status.bucketName` | string | set to external name when bucket Ready |
| `status.tableName` | string | left unset (R5.5) |
| `status.repositoryUrl` | string | left unset (R5.6) |

### Generated provider models (output)

From `dev.crossplane.io/models/io/upbound/m/aws/s3/v1beta1`:

- **`Bucket`** — `spec.forProvider` has `Region *string` and
  `Tags *map[string]string`; `APIVersion`/`Kind` are typed enums pinned to the
  `.m.` group. The AWS name comes from the `crossplane.io/external-name`
  annotation, not a `forProvider` field.
- **`BucketVersioning`** — `spec.forProvider` has `Bucket *string` (the bucket
  name to attach to), `BucketRef`/`BucketSelector` (unused here), and
  `VersioningConfiguration.Status *string` (`Enabled`/`Suspended`).

These are consumed as typed Go values and never hand-edited (R1.3).

### Internal model — `Names`

The pure `Names` struct (above) is the only bespoke data model. It carries the
derived `Namespace` and `BucketName`, both computed deterministically from
`tenant` and `environment`.

## Correctness Properties

*A property is a characteristic or behavior that should hold true across all valid executions of a system — a formal statement about what the system should do. Such properties serve as the bridge between human-readable specifications and machine-verifiable correctness guarantees.*

This slice suits property-based testing because the function's core is
**pure logic over a large input space**: names derived from tenant/environment,
the versioning enum mapping, the fixed tag set, the resource keyset, and the
status-from-readiness derivation. These vary meaningfully with input and are
cheap to run for hundreds of iterations. The AWS provider reconcile and the
`crossplane composition render` toolchain are **not** property-tested — they are
external behavior covered by integration and CI gates (see Testing Strategy).

The generators produce valid `TenantEnvironment` inputs (tenant matching the XRD
pattern `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$`, environment from
`dev`/`staging`/`prod`, region from the allow-list, and free `bucket.versioning`,
`table.enabled`, `repository.enabled` booleans) plus, for status, a mocked
observed-bucket readiness flag.

### Property 1: The bucket is always composed

*For any* valid `TenantEnvironment` — regardless of `spec.table.enabled` and
`spec.repository.enabled` — the desired-resources map contains the key `bucket`.

**Validates: Requirements 2.1, 2.2**

### Property 2: Bucket external name format and no metadata name derivation

*For any* valid tenant and environment, the composed bucket's
`crossplane.io/external-name` annotation equals the literal
`<tenant>-<env>-bucket`, and the bucket's `metadata.name` is not set to that value
nor to any other value derived from tenant/environment.

**Validates: Requirements 2.6, 3.1, 3.3, 3.4**

### Property 3: Name derivation is deterministic

*For any* tenant and environment, invoking `BuildNames` repeatedly produces a
byte-identical `BucketName` (and `Namespace`) every time.

**Validates: Requirements 3.2**

### Property 4: Naming rejects empty or whitespace input

*For any* tenant or environment string that is empty or whitespace-only,
`BuildNames` returns an error and produces no names, and the composition function
consequently omits the bucket from the desired resources.

**Validates: Requirements 2.7, 3.6**

### Property 5: Every composed resource uses the namespaced S3 group and correct Kind

*For any* valid `TenantEnvironment`, every emitted managed resource has an
`apiVersion` of `s3.aws.m.upbound.io/v1beta1` (never the legacy
`s3.aws.upbound.io`), and the `bucket` entry has Kind `Bucket` while the
`bucket-versioning` entry has Kind `BucketVersioning`.

**Validates: Requirements 2.3, 4.2, 6.4**

### Property 6: Versioning status maps from the spec flag

*For any* valid `TenantEnvironment`, the composed `BucketVersioning` has
`versioningConfiguration.status` equal to `Enabled` when `spec.bucket.versioning`
is `true` or absent, and `Suspended` when it is `false`, and to no other value.

**Validates: Requirements 4.3, 4.4, 4.5**

### Property 7: Namespace consistency and versioning-to-bucket reference

*For any* valid `TenantEnvironment`, the `bucket` and `bucket-versioning`
resources are both placed in namespace `<tenant>-<env>`, and the
`BucketVersioning` references the bucket by the bucket's external name
(`<tenant>-<env>-bucket`).

**Validates: Requirements 2.4, 4.6, 4.7**

### Property 8: Exactly two resources are composed

*For any* valid `TenantEnvironment`, the desired-resources map key set is exactly
`{ "bucket", "bucket-versioning" }` — no DynamoDB `Table`, no ECR `Repository`,
and no other key, regardless of `spec.table.enabled` or
`spec.repository.enabled`.

**Validates: Requirements 4.1, 6.1, 6.2, 6.3**

### Property 9: Composed resources carry exactly the standard tag set

*For any* valid `TenantEnvironment`, every composed resource that carries tags has
exactly the tag keys `tenant`, `environment`, and `managed-by` and no additional
key, with `tenant` and `environment` matching the XR spec.

**Validates: Requirements 6.5**

### Property 10: Status bucket name reflects current bucket readiness

*For any* valid `TenantEnvironment`, when the observed bucket reports Ready the
function sets `status.bucketName` to exactly the bucket external name (no prefix,
suffix, or whitespace); when the observed bucket is not Ready, `status.bucketName`
is left unset; and in all cases `status.tableName` and `status.repositoryUrl`
remain unset.

**Validates: Requirements 5.1, 5.2, 5.3, 5.4, 5.5, 5.6**

### Property 11: Invalid spec yields a fatal result and no resources

*For any* `TenantEnvironment` whose required spec fields cannot be read or are
invalid (missing/malformed `tenant` or `environment`), the function response
carries a fatal result and contains zero desired managed resources.

**Validates: Requirements 7.1**

## Error Handling

All user-facing problems surface on the XR so tenants can diagnose failures from
`kubectl describe`/events rather than reading the function pod log (R7.2).

- **Fatal, unrecoverable input problems** (R7.1): the XR cannot be read, or a
  required field (`tenant`, `environment`) is missing or fails validation, or
  `BuildNames` returns an error. The function calls `response.Fatal(rsp, err)` and
  returns **without** emitting any desired resources. `response.Fatal` attaches
  the result to the composite so it appears in the XR's events (R7.2).
- **Internal consistency guard** (R4.8): if the `bucket` entry is somehow absent
  when assembling `bucket-versioning`, the function does not emit the versioning
  resource and surfaces an error identifying the missing bucket. In this slice the
  bucket is unconditional, so this path is defensive.
- **Non-fatal conditions** (R7.4): reported via `response.Warning(rsp, ...)` with a
  message identifying the affected resource, after which the function continues
  emitting the remaining desired resources.
- **Error wrapping** (R7.3): internal errors are wrapped with
  `github.com/crossplane/crossplane-runtime/v2/pkg/errors` (e.g.
  `errors.Wrap(err, "cannot derive bucket external name")`) before being passed to
  `response.Fatal`/`response.Warning`, preserving context in the surfaced message.

Because the function refuses to emit a bucket with a malformed external name, a
bad tenant/environment never reaches AWS as a half-built resource. (A globally
unique S3 name collision — a 409 with `Synced=False` — is an AWS-side condition,
not a function bug, and is out of scope for this handling.)

## Testing Strategy

A dual approach: property-based tests for the pure logic, example-based unit
tests for specific wiring and edge cases, and `crossplane composition render` as
the integration gate.

### Property-based tests (the function's pure logic)

- Library: **`pgregory.net/rapid`** (idiomatic Go property testing that integrates
  with `go test`). Property tests are **not** hand-rolled.
- Each of Properties 1–11 is implemented as a **single** property-based test.
- Each property test runs a **minimum of 100 iterations** (rapid's default check
  count is well above this; configured explicitly to ≥100).
- Each test is tagged with a comment referencing its design property, format:
  `// Feature: tenant-environment-s3, Property {n}: {property text}`.
- Generators build valid `TenantEnvironment` inputs (tenant per the XRD pattern,
  environment/region from their enums, free booleans for the three flags) and, for
  Property 10, a mocked observed-bucket readiness flag.
- Properties 2, 3, 4 exercise `naming.go` directly (no Crossplane imports needed);
  Properties 1, 5–11 exercise `RunFunction` and inspect the returned desired
  resources / status.
- Comparisons use `github.com/google/go-cmp/cmp` where structural equality is
  asserted.

### Unit tests (examples, edge cases, wiring)

Table-driven (`map[string]struct{...}` + `t.Run`), per steering conventions:

- **`naming_test.go`**: representative and boundary cases for `BuildNames`,
  `VersioningStatus`, and `StandardTags` — including the shortest and longest
  tenants allowed by the pattern, each environment value, and empty/whitespace
  inputs (satisfies R8.3's "at least one table-driven test exercising the naming
  module").
- **`fn_test.go`**:
  - The two example XRs' expected desired output (`acme-dev`: versioning
    defaulted/enabled, table & repo disabled; `globex-prod`: versioning explicitly
    true) compared with `go-cmp`.
  - Error wiring (R7.2): a malformed XR produces a `Fatal` result targeted at the
    composite and zero desired resources.
  - The R4.8 guard path (bucket missing → no versioning + error), driven by a
    crafted internal state.

### Integration gate — composition render (R8.4, R8.6)

`crossplane composition render` over **every** file in
`examples/tenantenvironments/` is the end-to-end check. It builds the embedded
function, runs the pipeline, and prints the composed resources:

```bash
crossplane composition render \
  examples/tenantenvironments/acme-dev.yaml \
  apis/tenantenvironments/composition.yaml

crossplane composition render \
  examples/tenantenvironments/globex-prod.yaml \
  apis/tenantenvironments/composition.yaml
```

Both must exit `0` and emit no error; a failure names the offending example and
fails the definition of done. This is 1–2 representative examples by design —
render exercises external CLI/provider machinery, so more iterations add no value
(the input-varying logic is already covered by the property tests).

### CI gates (R8.1, R8.2, R8.3, R8.5)

Run in the PR job (never pushing):

```bash
cd functions/compose-tenant-environment && gofmt -l . && go vet ./... && go test ./...
```

- `gofmt -l .` must print nothing (R8.1).
- `go vet ./...` must be clean (R8.2).
- `go test ./...` must pass, including the property and table-driven tests (R8.3).
- `naming.go` importing zero Crossplane packages is a structural guarantee of the
  file split (R8.5); the build fails if a Crossplane import leaks in, and it is
  reinforced in review.

### Why some criteria are not property-tested

Requirement 1 (dependency add, model generation, cache) and Requirement 8's
tooling gates are **SMOKE/INTEGRATION** concerns — one-shot checks of external
tools with no meaningful input variation — verified by the presence of the
dependency, a compiling function that imports the generated models, and the CI
commands above. Requirement 7.3 (error-wrapping convention) and R7.4's warning
path are verified by unit tests and review rather than universal properties.
