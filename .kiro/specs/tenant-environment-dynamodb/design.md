# Design Document

## Overview

This slice adds the **DynamoDB vertical** on top of the already-shipped S3 + ECR
`TenantEnvironment` function, completing the product's resource trio. Applying a
`TenantEnvironment` with `spec.table.enabled: true` now provisions, in addition
to the unconditional S3 `Bucket` + `BucketVersioning` and the optional ECR
`Repository`, a namespaced DynamoDB **`Table`**, and populates
`status.tableName` once that table reports ready.

The `TenantEnvironment` API shape is unchanged: `spec.table.enabled`,
`spec.table.hashKey`, `spec.table.billingMode`, and `status.tableName` already
exist in the hand-maintained XRD (`apis/tenantenvironments/definition.yaml`).
This design implements the composition behavior behind them. It reuses the
existing embedded Go function without restructuring: the same `RunFunction`, the
same deferred desired-map conversion, the same pure `naming.go` split. The change
is additive and small:

1. Declare **`provider-aws-dynamodb`** (v2.x) as a project dependency (its typed
   Go models are generated under `dev.crossplane.io/models` by
   `crossplane dependency add`).
2. Extend `naming.go` so `Names`/`BuildNames` also produce the
   `<tenant>-<env>-dtbl` external name — still with **no Crossplane imports**.
3. Extend `fn.go`: read `spec.table.enabled` (absent ⇒ `false`), and when
   `true`, add a typed `*dynamodbv1beta1.Table` to the desired map under the
   stable key `table`; when `false`/absent, add nothing.
4. Extend the status path: when the table is enabled **and** the observed
   `Table` is Ready, set `status.tableName` to the derived external name;
   otherwise leave it absent — re-derived fresh every reconcile, never latched.
5. `examples/tenantenvironments/globex-prod.yaml` already enables the table;
   `acme-dev.yaml` keeps it disabled. Both must render clean.

Scope is **DynamoDB only**. S3 and ECR behavior is untouched. No IAM, KMS,
connection secrets, secondary indexes beyond the single hash key, point-in-time
recovery, streams, provisioned-throughput tuning beyond selecting `billingMode`,
GitOps, multi-region, or tags beyond `tenant` / `environment` / `managed-by`
(R7.8, R7.9).

Guiding principle: the simplest change that works end to end for DynamoDB,
reusing the S3 and ECR slices' patterns rather than restructuring.

### How this satisfies the requirements

| Requirement | Where addressed |
| ----------- | --------------- |
| R1 — Add DynamoDB provider dependency | Architecture → Dependency & models |
| R2 — Provision the table when enabled | Components → `fn.go` conditional emission, Table resource shape |
| R3 — Name via external-name | Components → `naming.go` `TableName`, Table external-name annotation |
| R4 — Key schema and billing mode from spec | Components → `fn.go` attribute / billing / capacity mapping |
| R5 — Tag with the standard tag set | Components → Table tags via `StandardTags` |
| R6 — Report `status.tableName` on ready | Components → status population (derived external name) |
| R7 — Preserve S3 + ECR, restrict slice to DynamoDB | Components → exact-keyset invariant; no new fields; `.m.` group only |
| R8 — Surface errors via response | Error Handling |
| R9 — Definition of done | Testing Strategy |

## Architecture

```
TenantEnvironment (XR)                 namespace: <tenant>-<env>
  apiVersion: platform.hello-crossplane.io/v1alpha1
  spec: { tenant, environment, region, bucket.versioning, table.{enabled,hashKey,billingMode}, repository.enabled }
        │
        ▼
Composition  apis/tenantenvironments/composition.yaml   (unchanged by this slice)
  mode: Pipeline
  ├─ step: compose-tenant-environment   (embedded Go function — extended here)
  │     reads observed XR → derives names → emits desired MRs → writes status
  └─ step: function-auto-ready          (marks XR Ready when composed MRs are Ready)
        │
        ▼
Desired managed resources               namespace: <tenant>-<env>
  map key "bucket"             → Bucket             (s3.aws.m.upbound.io/v1beta1)        [always]
  map key "bucket-versioning"  → BucketVersioning   (s3.aws.m.upbound.io/v1beta1)        [always]
  map key "repository"         → Repository         (ecr.aws.m.upbound.io/v1beta1)       [iff repository.enabled]
  map key "table"              → Table              (dynamodb.aws.m.upbound.io/v1beta1)  [iff table.enabled]
        │
        ▼  provider reconcile loop (~1 min)
AWS: S3 bucket + versioning  ·  ECR repo (when enabled)  ·  DynamoDB table "<tenant>-<env>-dtbl" (when enabled)
        │
        ▲
   provider reports observed Table Ready condition
   → function mirrors the derived external name into status.tableName when Ready
```

Everything is namespaced. The XR is a Crossplane **v2 namespaced composite**
(`apiextensions.crossplane.io/v2`, `scope: Namespaced`, no claims). A namespaced
XR composes only namespaced managed resources — the `.m.` API groups. The
legacy cluster-scoped `dynamodb.aws.upbound.io` group compiles but fails at
apply and is never used here (R7.7).

The composition pipeline itself does not change: `compose-tenant-environment`
still emits the desired resources and writes status; `function-auto-ready` still
sets the XR `Ready` condition once every composed MR is Ready. Adding the `Table`
to the desired map is enough for auto-ready to fold it into readiness — the
mechanism behind R6's "when the Table reports ready".

### Dependency and generated models (R1)

`provider-aws-dynamodb` is declared in `crossplane-project.yaml` and added via:

```bash
crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-dynamodb
crossplane dependency update-cache
```

`dependency add` regenerates the typed Go models under the
`dev.crossplane.io/models` module (rooted at `schemas/go`), alongside the
existing S3 and ECR models. The namespaced DynamoDB type the function needs is
then generated at:

- `dev.crossplane.io/models/io/upbound/m/aws/dynamodb/v1beta1` — `Table`
  (R1.2).

The new `crossplane-project.yaml` dependency entry follows the S3/ECR entries:

```yaml
- type: xpkg
  xpkg:
    apiVersion: pkg.crossplane.io/v1
    kind: Provider
    package: xpkg.crossplane.io/crossplane-contrib/provider-aws-dynamodb
    version: '>=v0.0.0'
```

(The scaffolded `>=v0.0.0` resolves to the latest v2 provider; the intent is
v2.x per steering, matching R1.1.) The models are **tool-generated and never
hand-edited** (R1.3); if the `Table` type were missing, the fix is to re-add the
dependency and regenerate, never to edit the files (R1.4).
`crossplane dependency update-cache` populates the local cache so a subsequent
build/render resolves the provider without a network fetch; if the cache is not
populated, the build/render fails identifying the unresolved package (R1.5,
R1.6) — a toolchain concern, not function logic.

### The `.m.` group choice

| Resource | apiVersion | Kind | Map key | This slice |
| -------- | ---------- | ---- | ------- | ---------- |
| S3 bucket | `s3.aws.m.upbound.io/v1beta1` | `Bucket` | `bucket` | unchanged |
| S3 versioning | `s3.aws.m.upbound.io/v1beta1` | `BucketVersioning` | `bucket-versioning` | unchanged |
| ECR repo | `ecr.aws.m.upbound.io/v1beta1` | `Repository` | `repository` | unchanged |
| DynamoDB table | `dynamodb.aws.m.upbound.io/v1beta1` | `Table` | `table` | **added, conditional** |

Provider Kinds are the upstream defaults — `Table`, not renamed. Map keys are
stable identifiers; renaming them orphans live AWS resources, so `table` is fixed
by contract (R2.7, R3.3).

## Components and Interfaces

The file layout is unchanged; this slice edits `naming.go` and `fn.go` and adds
sibling property-test files:

```
functions/compose-tenant-environment/
  fn.go          RunFunction — extended: conditional Table + status.tableName
  naming.go      pure string-building + validation — extended: TableName (NO Crossplane imports)
  fn_test.go     table-driven tests for RunFunction
  naming_test.go table-driven tests for naming.go — extended: TableName cases
  fn_*_property_test.go       rapid property tests — P-DDB-1..P-DDB-11 added as siblings
  naming_*_property_test.go   rapid property tests for naming.go
  go.mod go.sum  module for the embedded function
```

`apis/tenantenvironments/composition.yaml` is **unchanged** — the pipeline
already routes to `compose-tenant-environment` then `function-auto-ready`. Do not
re-run `crossplane composition generate` (it rewrites the pipeline).

### `naming.go` change — `TableName` (R3, R3.5)

The pure module gains one field on `Names` and one line in `BuildNames`. No new
function, no Crossplane imports (R9.5), and the existing determinism/validation
guarantees extend to the new name for free:

```go
// Names holds the deterministic identifiers derived from a TenantEnvironment.
type Names struct {
    Namespace      string // "<tenant>-<env>"
    BucketName     string // "<tenant>-<env>-bucket"  (the S3 external name)
    RepositoryName string // "<tenant>-<env>-ecr"     (the ECR external name)
    TableName      string // "<tenant>-<env>-dtbl"    (the DynamoDB external name)
}

func BuildNames(tenant, environment string) (Names, error) {
    // ... existing empty/whitespace validation on tenant and environment ...
    namespace := tenant + "-" + environment
    return Names{
        Namespace:      namespace,
        BucketName:     namespace + "-bucket",
        RepositoryName: namespace + "-ecr",
        TableName:      namespace + "-dtbl",
    }, nil
}
```

- `TableName` is the literal concatenation `<tenant>-<env>-dtbl` (R3.1).
- Deterministic: identical inputs always yield a byte-identical result — no time,
  randomness, or map iteration (R3.2).
- Empty/whitespace-only `tenant` or `environment` still yields an error and a
  zero-value `Names` (so `TableName == ""`), which drives the function's fatal
  path (R3.5). The single existing validation gate covers all four names.

`VersioningStatus` and `StandardTags` are unchanged; the Table reuses
`StandardTags(tenant, environment)` verbatim.

### `fn.go` change — conditional Table emission (R2, R3, R4, R5, R7)

Two edits to the existing `RunFunction`, keeping the deferred-conversion flow
intact:

1. A new stable key constant alongside the existing ones:

   ```go
   const (
       keyBucket           resource.Name = "bucket"
       keyBucketVersioning resource.Name = "bucket-versioning"
       keyRepository       resource.Name = "repository"
       keyTable            resource.Name = "table"
   )
   ```

2. After the Repository block (unchanged), read the enabled flag and
   conditionally add the Table. `spec.table.enabled` is read via `fieldpath`
   exactly like `spec.repository.enabled`: absent ⇒ `false` (the schema
   default), a read error other than not-found ⇒ fatal. `hashKey` and
   `billingMode` are read the same way, falling back to their schema defaults
   (`id`, `PAY_PER_REQUEST`) when absent.

   ```go
   // Resolve table.enabled. Absent means false (the schema default) → no
   // Table composed (R2.2, R2.3).
   tableEnabled := false
   if v, verr := paved.GetBool("spec.table.enabled"); verr != nil {
       if !fieldpath.IsNotFound(verr) {
           response.Fatal(rsp, errors.Wrap(verr, "cannot read spec.table.enabled from observed XR"))
           return rsp, nil
       }
   } else {
       tableEnabled = v
   }

   if tableEnabled {
       // Resolve hashKey (default "id") and billingMode (default
       // "PAY_PER_REQUEST"), each tolerating an absent value (R4.2, R4.5).
       hashKey := defaultHashKey
       if v, verr := paved.GetString("spec.table.hashKey"); verr != nil {
           if !fieldpath.IsNotFound(verr) {
               response.Fatal(rsp, errors.Wrap(verr, "cannot read spec.table.hashKey from observed XR"))
               return rsp, nil
           }
       } else {
           hashKey = v
       }

       billingMode := defaultBillingMode
       if v, verr := paved.GetString("spec.table.billingMode"); verr != nil {
           if !fieldpath.IsNotFound(verr) {
               response.Fatal(rsp, errors.Wrap(verr, "cannot read spec.table.billingMode from observed XR"))
               return rsp, nil
           }
       } else {
           billingMode = v
       }

       desired[keyTable] = buildTable(names, region, tags, hashKey, billingMode)
   }
   ```

`buildTable` assembles the typed model (kept as a small helper so `RunFunction`
stays readable; it takes only plain values, no Crossplane types beyond the
generated model):

```go
func buildTable(names Names, region string, tags map[string]string, hashKey, billingMode string) *dynamodbv1beta1.Table {
    fp := &dynamodbv1beta1.TableSpecForProvider{
        Region:      ptr(region),
        Tags:        ptr(tags),
        HashKey:     ptr(hashKey),
        BillingMode: ptr(billingMode),
        Attribute: ptr([]dynamodbv1beta1.TableSpecForProviderAttributeItem{
            {Name: ptr(hashKey), Type: ptr("S")},
        }),
    }
    // Provisioned mode requires explicit capacities; on-demand must leave them
    // unset (R4.6, R4.7).
    if billingMode == billingModeProvisioned {
        fp.ReadCapacity = ptr(float64(1))
        fp.WriteCapacity = ptr(float64(1))
    }
    return &dynamodbv1beta1.Table{
        APIVersion: ptr(dynamodbv1beta1.TableAPIVersionDynamodbAwsMUpboundIoV1Beta1),
        Kind:       ptr(dynamodbv1beta1.TableKindTable),
        Metadata: &metav1.ObjectMeta{
            Namespace: ptr(names.Namespace),
            Annotations: ptr(map[string]string{
                externalNameAnnotation: names.TableName,
            }),
        },
        Spec: &dynamodbv1beta1.TableSpec{ForProvider: fp},
    }
}
```

Supporting constants added near `defaultRegion`:

```go
const (
    defaultHashKey         = "id"
    defaultBillingMode     = "PAY_PER_REQUEST"
    billingModeProvisioned = "PROVISIONED"
)
```

Resource shape and the invariants it encodes:

- `apiVersion: dynamodb.aws.m.upbound.io/v1beta1`, `kind: Table` (R2.4, R7.7) —
  carried by the model's typed `APIVersion`/`Kind` constants, so the legacy
  cluster-scoped group cannot be selected by accident.
- `metadata.namespace: <tenant>-<env>` (R2.5) — same namespace as the Bucket and
  the XR (`names.Namespace`).
- `metadata.annotations["crossplane.io/external-name"] = <tenant>-<env>-dtbl`
  (R2.8, R3.3). **`metadata.name` is never set** to the external name or any
  tenant/env-derived value (R2.8, R3.4); it is left to the composition machinery,
  exactly as for the Bucket and Repository.
- `spec.forProvider.region = spec.region` (R2.6) — the same resolved `region`
  used for the Bucket (XRD default `ap-southeast-1` when absent).
- `spec.forProvider.hashKey = spec.table.hashKey` (default `id`) and exactly one
  `spec.forProvider.attribute` item `{name: <hashKey>, type: "S"}` (R4.1, R4.2,
  R4.3). Only the hash key is declared as an attribute — DynamoDB requires each
  attribute to be a key attribute, so no extra attributes are added (R7.8: no
  secondary indexes).
- `spec.forProvider.billingMode = spec.table.billingMode` (default
  `PAY_PER_REQUEST`) (R4.4, R4.5). `readCapacity`/`writeCapacity` are set to `1`
  **only** when `billingMode == PROVISIONED`, and left unset for
  `PAY_PER_REQUEST` (R4.6, R4.7).
- `spec.forProvider.tags = {tenant, environment, managed-by}` (R5.1–R5.3, R7.9)
  via the existing `StandardTags(tenant, environment)` — the same map the Bucket
  and Repository use.
- No other `forProvider` field is set — no `serverSideEncryption`,
  `pointInTimeRecovery`, `streamEnabled`, `globalSecondaryIndex`,
  `localSecondaryIndex`, `ttl`, etc. — keeping the slice free of
  IAM/KMS/secret/GSI/PITR/stream scope (R7.8).

The Bucket, BucketVersioning, and Repository assembly, the R-versioning guard,
and the deferred `toDesiredComposed` conversion are all **unchanged** (R7.1,
R7.2, R7.3). The result is the exact keyset invariant: two resources when only
S3, three with either ECR or DynamoDB, four when both are enabled (R7.4, R7.5,
R7.6).

### `fn.go` change — status population (R6)

`status.tableName` mirrors the S3 `status.bucketName` pattern exactly: the value
is **derivable from naming** (`names.TableName`), unlike ECR's
`status.repositoryUrl` which must be read from the observed resource. So the
table path sets the derived external name when the observed `Table` is Ready — no
`atProvider` read needed.

`populateStatus` is extended (still a single call from `RunFunction`, still
initialising the desired composite from the observed one so untouched status
fields are preserved). It keeps the existing `status.bucketName` and
`status.repositoryUrl` behavior unchanged and adds a parallel path for
`status.tableName`. The signature gains the enabled flag so the disabled case is
handled without inspecting observed resources:

```go
func (f *Function) populateStatus(req *fnv1.RunFunctionRequest, rsp *fnv1.RunFunctionResponse, names Names, repoEnabled, tableEnabled bool) error {
    observed, err := request.GetObservedComposedResources(req)
    if err != nil {
        return errors.Wrap(err, "cannot get observed composed resources")
    }

    dxr, err := request.GetDesiredCompositeResource(req)
    if err != nil {
        return errors.Wrap(err, "cannot get desired composite resource")
    }
    paved := fieldpath.Pave(dxr.Resource.Object)

    // --- status.bucketName (unchanged S3 behavior) ---
    // --- status.repositoryUrl (unchanged ECR behavior) ---
    //   ... existing blocks ...

    // --- status.tableName (this slice) ---
    // Set ONLY when the table is enabled AND the observed Table is Ready.
    // Otherwise leave the field absent. Derived fresh every reconcile from the
    // current observed state — never latched — so enabled→disabled or
    // ready→not-ready naturally clears it (R6.2, R6.3, R6.5, R6.6).
    if tableEnabled {
        if table, ok := observed[keyTable]; ok &&
            table.Resource.GetCondition(xpv2.TypeReady).Status == corev1.ConditionTrue {
            if err := paved.SetString("status.tableName", names.TableName); err != nil {
                return errors.Wrap(err, "cannot set status.tableName on desired composite resource")
            }
        }
    }

    return response.SetDesiredCompositeResource(rsp, dxr)
}
```

Status contract encoded here:

- Table enabled and observed Ready ⇒ `status.tableName` equals `names.TableName`
  (`<tenant>-<env>-dtbl`) **byte-for-byte** (R6.1, R6.4).
- Table not Ready, not observed, or not enabled ⇒ field left absent (R6.2, R6.5).
- The value is always recomputed from the current observed Ready condition;
  nothing is latched, so ready→not-ready clears it (R6.3, R6.6).

Because the disabled branch never reads the observed Table, a stale observed
Table from a prior enabled reconcile cannot leak into status once disabled.

## Data Models

### TenantEnvironment (input) — already defined

The XRD (`definition.yaml`) is the source of truth and is unchanged by this
slice. Fields read by the function:

| Field | Type | Default | Used for |
| ----- | ---- | ------- | -------- |
| `spec.tenant` | string (immutable, pattern) | required | name derivation, tags |
| `spec.environment` | enum `dev`/`staging`/`prod` (immutable) | required | name derivation, tags |
| `spec.region` | enum allow-list | `ap-southeast-1` | `forProvider.region` on all resources |
| `spec.bucket.versioning` | bool | `true` | versioning status mapping (S3) |
| `spec.repository.enabled` | bool | `false` | gates the ECR Repository (unchanged) |
| `spec.table.enabled` | bool | `false` | **gates the DynamoDB Table (this slice)** |
| `spec.table.hashKey` | string | `id` | **Table `forProvider.hashKey` + attribute name** |
| `spec.table.billingMode` | enum `PAY_PER_REQUEST`/`PROVISIONED` | `PAY_PER_REQUEST` | **Table `forProvider.billingMode` + capacity gating** |

Status contract written by the function:

| Field | Type | This slice |
| ----- | ---- | ---------- |
| `status.bucketName` | string | set to external name when bucket Ready (unchanged) |
| `status.repositoryUrl` | string | set to observed `atProvider.repositoryUrl` when repo enabled + Ready (unchanged) |
| `status.tableName` | string | **set to `<tenant>-<env>-dtbl` when table enabled + Ready** |

### Generated provider model (output)

From `dev.crossplane.io/models/io/upbound/m/aws/dynamodb/v1beta1`, following the
same code-generation pattern as the S3 and ECR models:

- **`Table`** — top-level typed `APIVersion *TableAPIVersion` and
  `Kind *TableKind` (constants `TableAPIVersionDynamodbAwsMUpboundIoV1Beta1` =
  `dynamodb.aws.m.upbound.io/v1beta1` and `TableKindTable` = `Table`),
  `Metadata *metav1.ObjectMeta`, `Spec *TableSpec`, `Status *TableStatus`.
- **`TableSpec.ForProvider` (`*TableSpecForProvider`)** — the fields this slice
  sets are `Region *string`, `Tags *map[string]string`, `HashKey *string`,
  `BillingMode *string`, `Attribute *[]TableSpecForProviderAttributeItem`, and
  (provisioned only) `ReadCapacity *float64` / `WriteCapacity *float64`. All
  other fields (`ServerSideEncryption`, `PointInTimeRecovery`, `StreamEnabled`,
  `GlobalSecondaryIndex`, `LocalSecondaryIndex`, `Ttl`, `ForceDestroy`, …) are
  left nil (R7.8).
- **`TableSpecForProviderAttributeItem`** — `Name *string`, `Type *string`. The
  single item declares the hash key with type `"S"` (R4.3).
- **`TableStatus`** — the function reads only the composed resource's Ready
  condition (via the SDK's `GetCondition`), not any `atProvider` field, because
  the table name is derived, not provider-reported.

The exact generated Go identifiers (constant names, whether capacities are
`*float64` vs `*int`) come from the tool output; the design assumes the standard
upjet-generated shape used by the S3/ECR models and adapts at implementation time
if the generator differs. These models are consumed as typed Go values and never
hand-edited (R1.3).

### Internal model — `Names`

The pure `Names` struct (above) is the only bespoke data model. It gains a
`TableName` field, computed deterministically from `tenant` and `environment`
alongside `Namespace`, `BucketName`, and `RepositoryName`.

## Correctness Properties

*A property is a characteristic or behavior that should hold true across all valid executions of a system — a formal statement about what the system should do. Such properties serve as the bridge between human-readable specifications and machine-verifiable correctness guarantees.*

This slice suits property-based testing for the same reason the S3 and ECR slices
did: the function's core is **pure logic over a large input space** — the derived
DynamoDB name, the conditional keyset, the key-schema and billing/capacity
mapping, the fixed tag set, the group/Kind selection, and the status-from-
observed-state derivation. These vary meaningfully with input and are cheap to
run for hundreds of iterations. Provider reconcile and the
`crossplane composition render` toolchain are **not** property-tested — they are
external behavior covered by integration and CI gates (see Testing Strategy).

Generators produce valid `TenantEnvironment` inputs (tenant matching the XRD
pattern `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$`, environment from
`dev`/`staging`/`prod`, region from the allow-list, `hashKey` a non-empty
identifier, `billingMode` from `{PAY_PER_REQUEST, PROVISIONED}`, and free
`bucket.versioning`, `table.enabled`, `repository.enabled` booleans — including
branches where `table.enabled`, `table.hashKey`, and `table.billingMode` are
*absent* to exercise the defaults). For status, the generator additionally
produces a mocked observed `Table` with a Ready flag.

### Property 1: Exact resource keyset reflects the enabled flags

*For any* valid `TenantEnvironment` input: when `spec.table.enabled` is `false`
or absent, `RunFunction` does not emit the `table` key; when `spec.table.enabled`
is `true`, it emits exactly one `table` resource in addition to the always-present
`bucket` and `bucket-versioning` (and the `repository` iff
`spec.repository.enabled` is `true`), and no other key.

*Verification:* build a valid XR varying all flags (including absent table/repo
flags), run `RunFunction`, read the response's desired composed resources, and
assert the key set equals `{bucket, bucket-versioning}` plus `repository` and/or
`table` per the flags. Mirrors the existing `fn_ecr_keyset_property_test.go`
harness, generalised over `table.enabled`.

**Validates: Requirements 2.1, 2.2, 2.3, 2.7, 5.4, 7.4, 7.5, 7.6**

_Property identifier: P-DDB-1_

### Property 2: Table group and Kind are correct

*For any* valid input with `spec.table.enabled` `true`, the composed `table`
resource has `apiVersion: dynamodb.aws.m.upbound.io/v1beta1` and `Kind: Table`,
never the legacy cluster-scoped group `dynamodb.aws.upbound.io`.

*Verification:* when enabled, read the `table` entry's `apiVersion`/`kind` from
the desired output and assert exact equality; assert the apiVersion is not
`dynamodb.aws.upbound.io/...`.

**Validates: Requirements 2.4, 7.7**

_Property identifier: P-DDB-2_

### Property 3: Table external-name equals `<tenant>-<env>-dtbl`

*For any* valid input with `spec.table.enabled` `true`, the composed `table`
carries a `crossplane.io/external-name` annotation exactly equal to
`<tenant>-<env>-dtbl`, its `spec.forProvider.region` equals `spec.region`, and its
`metadata.name` is neither that external name nor any other value derived from
tenant/environment.

*Verification:* when enabled, read the `table` annotation and assert it equals
`tenant + "-" + environment + "-dtbl"`; assert `metadata.name` is unset (or at
least not equal to the external name nor `<tenant>-<env>`); assert
`spec.forProvider.region` equals the drawn region.

**Validates: Requirements 2.6, 2.8, 3.1, 3.3, 3.4**

_Property identifier: P-DDB-3_

### Property 4: Table namespace equals `<tenant>-<env>`

*For any* valid input with `spec.table.enabled` `true`, the composed `table`
lands in the namespace `<tenant>-<env>`, the same namespace as the Bucket.

*Verification:* when enabled, read `table.metadata.namespace` and assert it
equals `tenant + "-" + environment` and matches the bucket's namespace.

**Validates: Requirements 2.5**

_Property identifier: P-DDB-4_

### Property 5: Key schema mirrors the spec

*For any* valid input with `spec.table.enabled` `true`, the composed `table` has
`spec.forProvider.hashKey` equal to `spec.table.hashKey` (defaulting to `id` when
absent), and declares exactly one `spec.forProvider.attribute` item whose `name`
equals that hash key and whose `type` is `S`.

*Verification:* when enabled (varying present/absent `hashKey`), read
`forProvider.hashKey` and assert it equals the drawn hash key (or `id` when
absent); read the attribute list, assert length `1`, `attribute[0].name` equals
the hash key, and `attribute[0].type` equals `"S"`.

**Validates: Requirements 4.1, 4.2, 4.3**

_Property identifier: P-DDB-5_

### Property 6: Billing mode and capacity are consistent

*For any* valid input with `spec.table.enabled` `true`, the composed `table` has
`spec.forProvider.billingMode` equal to `spec.table.billingMode` (defaulting to
`PAY_PER_REQUEST` when absent); where the mode is `PAY_PER_REQUEST` no
`readCapacity`/`writeCapacity` is set, and where the mode is `PROVISIONED` both
are set to `1`.

*Verification:* when enabled (varying present/absent `billingMode` across both
enum values), read `forProvider.billingMode` and assert equality/default; assert
`readCapacity`/`writeCapacity` are unset for `PAY_PER_REQUEST` and both equal `1`
for `PROVISIONED`.

**Validates: Requirements 4.4, 4.5, 4.6, 4.7**

_Property identifier: P-DDB-6_

### Property 7: `status.tableName` mirrors the external name only when Ready

*For any* valid input paired with a mocked observed `Table`: when
`spec.table.enabled` is `true` **and** the observed Table reports Ready,
`status.tableName` equals the `<tenant>-<env>-dtbl` external name byte-for-byte;
otherwise (`enabled` false, not observed, or not Ready) `status.tableName` is
absent. The value is re-derived from the current observed state every reconcile
and never latched.

*Verification:* mirror `fn_ecr_status_repourl_property_test.go`. Build a request
whose observed composed resources include a mocked `table` with a Ready condition
(`True`/`False`/absent); run `RunFunction`; read `status.tableName` off the
desired composite. Assert exact equality with `names.TableName` in the "enabled +
Ready" case, and absence in every other case.

**Validates: Requirements 6.1, 6.2, 6.3, 6.4, 6.5, 6.6**

_Property identifier: P-DDB-7_

### Property 8: Table tag set is exactly the three standard tags

*For any* valid input with `spec.table.enabled` `true`, the composed `table`
`spec.forProvider.tags` has exactly the keys `tenant`, `environment`, and
`managed-by`, with `tenant`/`environment` equal to the source spec fields and
`managed-by` the fixed literal `crossplane`; when disabled, no `table` (and
therefore no such tags) exists.

*Verification:* when enabled, read the `table` tags map, assert the sorted key
set equals `{environment, managed-by, tenant}`, and assert the values; when
disabled, assert the `table` key is absent (folds into Property 1).

**Validates: Requirements 5.1, 5.2, 5.3, 5.4, 7.9**

_Property identifier: P-DDB-8_

### Property 9: Invalid spec emits zero resources

*For any* `TenantEnvironment` XR whose required spec fields are missing or
malformed such that `TableName` cannot be derived — spec absent, tenant/
environment key missing, tenant/environment empty/whitespace, or the observed
composite unreadable — `RunFunction` reports a fatal result and emits zero desired
managed resources.

*Verification:* reuse the existing `fn_ecr_invalid_spec_property_test.go` / P11
harness unchanged — the fatal-path invariant is not specific to DynamoDB
(`BuildNames` rejecting empty inputs already gates all four names, and the
deferred block emits nothing on a fatal result). Assert the response carries a
`SEVERITY_FATAL` result and the desired composed resource map is empty.

**Validates: Requirements 3.5, 8.1, 8.5**

_Property identifier: P-DDB-9_

### Property 10: Naming is deterministic and rejects empty inputs

*For any* valid `spec.tenant` and `spec.environment` pair, `BuildNames` produces a
byte-identical `TableName` on repeated invocations; if either input is empty or
whitespace-only, `BuildNames` returns an error and produces no name
(`TableName == ""`), identifying the missing field.

*Verification:* exercise `naming.go` directly (no Crossplane imports). Draw a
valid tenant/environment, call `BuildNames` twice, assert `TableName` is identical
and equals `<tenant>-<env>-dtbl`; draw empty/whitespace inputs and assert an error
with a zero-value `Names`.

**Validates: Requirements 3.1, 3.2, 3.5**

_Property identifier: P-DDB-10_

### Property 11: S3 and ECR behavior is unchanged

*For any* valid input, the composed `bucket`, `bucket-versioning`, and (where
`spec.repository.enabled` is `true`) `repository` resources are byte-for-byte
identical to the output produced with the table disabled, regardless of the
`spec.table.*` values.

*Verification:* run `RunFunction` twice on inputs that differ only in
`spec.table.*` (enabled/disabled, varying hashKey/billingMode), extract the
`bucket`, `bucket-versioning`, and `repository` entries from each desired map, and
assert they are equal with `go-cmp`. The `table` entry (present in one run only)
is excluded from the comparison.

**Validates: Requirements 7.1, 7.2, 7.3**

_Property identifier: P-DDB-11_

## Error Handling

All user-facing problems surface on the XR so tenants can diagnose failures from
`kubectl describe`/events rather than reading the function pod log (R8.2).

- **Fatal, unrecoverable input problems** (R8.1, R8.5): the observed XR cannot be
  read, a required field (`tenant`, `environment`) is missing or fails
  validation, `BuildNames` returns an error, or `spec.table.enabled`,
  `spec.table.hashKey`, or `spec.table.billingMode` cannot be read for a reason
  other than not-found. The function calls `response.Fatal(rsp, err)` and returns
  **without** emitting any desired resources — the deferred conversion
  short-circuits on a fatal result, so a bad input yields exactly zero managed
  resources (R8.1, Property 9). `response.Fatal` attaches the result to the
  composite so it appears in the XR's events (R8.2).
- **Non-fatal table conditions** (R8.4): reported via
  `response.Warning(rsp, ...)` with a message identifying the affected resource by
  its composition-resource key `table`, after which the function continues
  emitting the remaining desired resources unchanged. (In this PoC the table is
  composed unconditionally-once-enabled with no per-table validation, so this
  path is defensive — reserved for future table-specific warnings.)
- **Error wrapping** (R8.3): internal errors are wrapped with
  `github.com/crossplane/crossplane-runtime/v2/pkg/errors` (e.g.
  `errors.Wrap(verr, "cannot read spec.table.enabled from observed XR")` and
  `errors.Wrap(err, "cannot set status.tableName on desired composite resource")`)
  before being passed to `response.Fatal`/`response.Warning`, preserving the
  originating error text.

Absent-but-optional reads are **not** errors: a missing `spec.table.enabled` is
`fieldpath.IsNotFound` and defaults to `false`; a missing `spec.table.hashKey`
defaults to `id`; a missing `spec.table.billingMode` defaults to
`PAY_PER_REQUEST`; a missing observed Table simply leaves `status.tableName`
unset. Only unexpected read failures are surfaced.

Because the function refuses to emit a Table with a malformed external name (the
shared `BuildNames` gate), a bad tenant/environment never reaches AWS as a
half-built resource. (A name that is invalid AWS-side despite passing the schema
is a provider condition, not a function bug, and is out of this handling's scope.)

## Testing Strategy

A dual approach: property-based tests for the pure logic, example-based unit tests
for specific wiring and edge cases, and `crossplane composition render` as the
integration gate.

### Property-based tests (the function's pure logic)

- Library: **`pgregory.net/rapid`**, as used by the existing property tests.
  Property tests are **not** hand-rolled.
- Each of Properties P-DDB-1..P-DDB-11 is implemented as a **single**
  property-based test, in its own sibling file consistent with the existing
  `fn_*_property_test.go` / `naming_*_property_test.go` naming:

  | Property | Test file | Exercises |
  | -------- | --------- | --------- |
  | P-DDB-1 | `fn_ddb_keyset_property_test.go` | `RunFunction` desired keyset (2/3/4 keys) |
  | P-DDB-2 | `fn_ddb_group_kind_property_test.go` | `table` apiVersion/Kind |
  | P-DDB-3 | `fn_ddb_extname_property_test.go` | `table` external-name, region, no metadata.name |
  | P-DDB-4 | `fn_ddb_namespace_property_test.go` | `table` namespace |
  | P-DDB-5 | `fn_ddb_keyschema_property_test.go` | `table` hashKey + single attribute |
  | P-DDB-6 | `fn_ddb_billing_property_test.go` | `table` billingMode + capacity gating |
  | P-DDB-7 | `fn_ddb_status_tablename_property_test.go` | `status.tableName` from observed readiness |
  | P-DDB-8 | `fn_ddb_tags_property_test.go` | `table` tag set |
  | P-DDB-9 | `fn_ddb_invalid_spec_property_test.go` | fatal + zero resources (or reuse existing harness) |
  | P-DDB-10 | `naming_ddb_table_property_test.go` | `TableName` determinism + rejection |
  | P-DDB-11 | `fn_ddb_s3_ecr_unchanged_property_test.go` | S3/ECR output invariance across table flags |

- Each property test runs a **minimum of 100 iterations** (rapid's default check
  count exceeds this).
- Each test is tagged with a comment referencing its design property, format:
  `// Feature: tenant-environment-dynamodb, Property {n}: {property text}`.
- Generators reuse the existing tenant/environment/region generators and vary
  `table.enabled` (true/false/absent), `table.hashKey` (present/absent),
  `table.billingMode` (each enum value/absent), and `repository.enabled` across
  the input space. P-DDB-7's generator additionally produces a mocked observed
  `Table` with a Ready flag (`True`/`False`/absent).
- P-DDB-10 exercises `naming.go` directly (no Crossplane imports);
  P-DDB-1..P-DDB-9 and P-DDB-11 exercise `RunFunction` and inspect the returned
  desired resources / status.
- Comparisons use `github.com/google/go-cmp/cmp` where structural equality is
  asserted (e.g. the tag key set, the S3/ECR invariance check).

### Unit tests (examples, edge cases, wiring)

Table-driven (`map[string]struct{...}` + `t.Run`), per steering conventions:

- **`naming_test.go`**: extend `TestBuildNames` so the expected `Names` includes
  `TableName` for each case (shortest/longest tenant, each environment) —
  satisfying R9.3's "at least one table-driven test exercising the Naming_Module's
  Table_External_Name derivation". `StandardTags` is already covered; the Table
  reuses it.
- **`fn_test.go`**: extend the example-XR expectations:
  - `acme-dev` (table disabled, repository disabled) ⇒ desired keyset
    `{bucket, bucket-versioning}`, no `table`, `status.tableName` absent.
  - `globex-prod` (table enabled, repository enabled) ⇒ desired keyset
    `{bucket, bucket-versioning, repository, table}`; the `table` carries external
    name `globex-prod-dtbl`, namespace `globex-prod`, region `ap-southeast-1`,
    `hashKey: id`, one attribute `{id, S}`, `billingMode: PAY_PER_REQUEST` with no
    capacities, and the three standard tags.
  - A `PROVISIONED` example case (crafted inline, not necessarily an example file)
    asserting `readCapacity == writeCapacity == 1`.
  - Error wiring (R8.2): a malformed XR yields a `Fatal` result on the composite
    and zero desired resources (existing case, unchanged).
  - Warning wiring (R8.4): if a `response.Warning` path is added, a crafted case
    asserts the warning mentions `table` and the other resources are still
    emitted.

### Integration gate — composition render (R9.4, R9.6, R9.7)

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

- `acme-dev.yaml` (table disabled) must render the Bucket + BucketVersioning, no
  Table.
- `globex-prod.yaml` (table enabled) must render the Bucket + BucketVersioning +
  Repository + a `Table` carrying `crossplane.io/external-name: globex-prod-dtbl`
  (R9.7).

Both must exit `0` and emit no error; a failure names the offending example and
fails the definition of done (R9.6). This is 1–2 representative examples by
design — render exercises external CLI/provider machinery, so more iterations add
no value (the input-varying logic is already covered by the property tests).

### CI gates (R9.1, R9.2, R9.3, R9.5)

Run in the PR job (never pushing):

```bash
cd functions/compose-tenant-environment && gofmt -l . && go vet ./... && go test ./...
```

- `gofmt -l .` must print nothing (R9.1).
- `go vet ./...` must be clean (R9.2).
- `go test ./...` must pass, including the new DynamoDB property tests and the
  extended `naming_test.go` (R9.3).
- `naming.go` importing zero Crossplane packages is a structural guarantee of the
  file split (R9.5); the new `TableName` line adds only string concatenation, no
  imports. The build fails if a Crossplane import leaks in, reinforced in review.

### Why some criteria are not property-tested

Requirement 1 (dependency add, model generation, cache) and Requirement 9's
tooling gates are **SMOKE/INTEGRATION** concerns — one-shot checks of external
tools with no meaningful input variation — verified by the presence of the
dependency entry, a function that compiles against the generated DynamoDB model,
and the CI commands above. Requirement 7.8 (scope exclusions) is verified
structurally (the Table sets only `region`, `tags`, `hashKey`, `attribute`,
`billingMode`, and — provisioned only — capacities) plus render-output inspection.
Requirements 8.2, 8.3, and 8.4 (report attachment, error-wrapping convention,
warning path) are verified by unit tests and review rather than universal
properties.
