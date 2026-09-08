# Design Document

## Overview

This slice adds the **ECR vertical** on top of the already-shipped S3-only
`TenantEnvironment` function. Applying a `TenantEnvironment` with
`spec.repository.enabled: true` now provisions, in addition to the unconditional
S3 `Bucket` + `BucketVersioning`, a namespaced ECR **`Repository`**, and
populates `status.repositoryUrl` once that repository reports ready.

The `TenantEnvironment` API shape is unchanged: `spec.repository.enabled` and
`status.repositoryUrl` already exist in the hand-maintained XRD
(`apis/tenantenvironments/definition.yaml`). This design implements the
composition behavior behind them. It reuses the existing embedded Go function
without restructuring: the same `RunFunction`, the same deferred desired-map
conversion, the same pure `naming.go` split. The change is additive and small:

1. Declare **`provider-aws-ecr`** (v2.x) as a project dependency (its typed Go
   models are already generated under `dev.crossplane.io/models`).
2. Extend `naming.go` so `Names`/`BuildNames` also produce the
   `<tenant>-<env>-ecr` external name — still with **no Crossplane imports**.
3. Extend `fn.go`: read `spec.repository.enabled` (absent ⇒ `false`), and when
   `true`, add a typed `*ecrv1beta1.Repository` to the desired map under the
   stable key `repository`; when `false`/absent, add nothing.
4. Extend the status path: when the repository is enabled **and** the observed
   `Repository` is Ready **and** carries a non-whitespace
   `status.atProvider.repositoryUrl`, set `status.repositoryUrl` to that observed
   value; otherwise leave it absent — re-derived fresh every reconcile, never
   latched.
5. `examples/tenantenvironments/globex-prod.yaml` already enables the repository;
   `acme-dev.yaml` keeps it disabled. Both must render clean.

Scope is **ECR only**. S3 behavior is untouched; DynamoDB remains a separate
future slice. No IAM, KMS, connection secrets, image-scanning config, lifecycle
or repository policies, GitOps, multi-region, or tags beyond
`tenant` / `environment` / `managed-by`.

Guiding principle: the simplest change that works end to end for ECR, reusing the
S3 slice's patterns rather than restructuring.

### How this satisfies the requirements

| Requirement | Where addressed |
| ----------- | --------------- |
| R1 — Add ECR provider dependency | Architecture → Dependency & models; already present under `schemas/go` |
| R2 — Provision the repository when enabled | Components → `fn.go` conditional emission, Repository resource shape |
| R3 — Name via external-name | Components → `naming.go` `RepositoryName`, Repository external-name annotation |
| R4 — Tag with the standard tag set | Components → Repository tags via `StandardTags` |
| R5 — Report `status.repositoryUrl` on ready | Components → status population (observed atProvider) |
| R6 — Preserve S3, restrict slice to S3 + ECR | Components → exact-keyset invariant; no Table; `.m.` group only |
| R7 — Surface errors via response | Error Handling |
| R8 — Definition of done | Testing Strategy |

## Architecture

```
TenantEnvironment (XR)                 namespace: <tenant>-<env>
  apiVersion: platform.hello-crossplane.io/v1alpha1
  spec: { tenant, environment, region, bucket.versioning, table*, repository.enabled }
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
  map key "bucket"             → Bucket             (s3.aws.m.upbound.io/v1beta1)   [always]
  map key "bucket-versioning"  → BucketVersioning   (s3.aws.m.upbound.io/v1beta1)   [always]
  map key "repository"         → Repository         (ecr.aws.m.upbound.io/v1beta1)  [iff enabled]
        │
        ▼  provider reconcile loop (~1 min)
AWS: S3 bucket + versioning  ·  ECR repo "<tenant>-<env>-ecr" (when enabled)
        │
        ▲
   provider reports observed Repository.status.atProvider.repositoryUrl
   → function mirrors it into status.repositoryUrl when Ready
```

Everything is namespaced. The XR is a Crossplane **v2 namespaced composite**
(`apiextensions.crossplane.io/v2`, `scope: Namespaced`, no claims). A namespaced
XR composes only namespaced managed resources — the `.m.` API groups. The
legacy cluster-scoped `ecr.aws.upbound.io` group compiles but fails at apply and
is never used here (R6.6).

The composition pipeline itself does not change: `compose-tenant-environment`
still emits the desired resources and writes status; `function-auto-ready` still
sets the XR `Ready` condition once every composed MR is Ready. Adding the
`Repository` to the desired map is enough for auto-ready to fold it into
readiness — the mechanism behind R5's "when the Repository reports ready".

### Dependency and generated models (R1)

`provider-aws-ecr` is declared in `crossplane-project.yaml` and added via:

```bash
crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-ecr
crossplane dependency update-cache
```

`dependency add` regenerated the typed Go models under the
`dev.crossplane.io/models` module (rooted at `schemas/go`). The namespaced ECR
type the function needs already exists there:

- `dev.crossplane.io/models/io/upbound/m/aws/ecr/v1beta1` — `Repository`.

`schemas/.lock.json` pins the provider to `provider-aws-ecr:v2.7.0`, matching the
`>=v2.0.0,<v3.0.0` intent (R1.1). The models are **tool-generated and never
hand-edited** (R1.3); if the `Repository` type were missing, the fix is to re-add
the dependency and regenerate, never to edit the files (R1.4). The new
`crossplane-project.yaml` dependency entry is:

```yaml
- type: xpkg
  xpkg:
    apiVersion: pkg.crossplane.io/v1
    kind: Provider
    package: xpkg.crossplane.io/crossplane-contrib/provider-aws-ecr
    version: '>=v0.0.0'
```

(The scaffolded `>=v0.0.0` resolves to the latest v2 provider; the intent is
v2.x per steering, and the lock file already resolves `v2.7.0`.)
`crossplane dependency update-cache` populates the local cache so a subsequent
build/render resolves the provider without a network fetch; if the cache is not
populated, the build/render fails identifying the unresolved package (R1.5,
R1.6) — a toolchain concern, not function logic.

### The `.m.` group choice

| Resource | apiVersion | Kind | Map key | This slice |
| -------- | ---------- | ---- | ------- | ---------- |
| S3 bucket | `s3.aws.m.upbound.io/v1beta1` | `Bucket` | `bucket` | unchanged |
| S3 versioning | `s3.aws.m.upbound.io/v1beta1` | `BucketVersioning` | `bucket-versioning` | unchanged |
| ECR repo | `ecr.aws.m.upbound.io/v1beta1` | `Repository` | `repository` | **added, conditional** |

Provider Kinds are the upstream defaults — `Repository`, not renamed. Map keys
are stable identifiers; renaming them orphans live AWS resources, so `repository`
is fixed by contract (R2.7, R3.3).

## Components and Interfaces

The file layout is unchanged; this slice edits `naming.go` and `fn.go` and adds
sibling property-test files:

```
functions/compose-tenant-environment/
  fn.go          RunFunction — extended: conditional Repository + status.repositoryUrl
  naming.go      pure string-building + validation — extended: RepositoryName (NO Crossplane imports)
  fn_test.go     table-driven tests for RunFunction
  naming_test.go table-driven tests for naming.go — extended: RepositoryName cases
  fn_*_property_test.go  rapid property tests — P-ECR-1..P-ECR-8 added as siblings
  go.mod go.sum  module for the embedded function
```

`apis/tenantenvironments/composition.yaml` is **unchanged** — the pipeline
already routes to `compose-tenant-environment` then `function-auto-ready`. Do not
re-run `crossplane composition generate` (it rewrites the pipeline).

### `naming.go` change — `RepositoryName` (R3, R3.5)

The pure module gains one field on `Names` and one line in `BuildNames`. No new
function, no Crossplane imports (R8.5), and the existing determinism/validation
guarantees extend to the new name for free:

```go
// Names holds the deterministic identifiers derived from a TenantEnvironment.
type Names struct {
    Namespace      string // "<tenant>-<env>"
    BucketName     string // "<tenant>-<env>-bucket"  (the S3 external name)
    RepositoryName string // "<tenant>-<env>-ecr"     (the ECR external name)
}

func BuildNames(tenant, environment string) (Names, error) {
    // ... existing empty/whitespace validation on tenant and environment ...
    namespace := tenant + "-" + environment
    return Names{
        Namespace:      namespace,
        BucketName:     namespace + "-bucket",
        RepositoryName: namespace + "-ecr",
    }, nil
}
```

- `RepositoryName` is the literal concatenation `<tenant>-<env>-ecr` (R3.1).
- Deterministic: identical inputs always yield a byte-identical result — no time,
  randomness, or map iteration (R3.2).
- Empty/whitespace-only `tenant` or `environment` still yields an error and a
  zero-value `Names` (so `RepositoryName == ""`), which drives the function's
  fatal path (R3.5). The single existing validation gate covers all three names.

`VersioningStatus` and `StandardTags` are unchanged; the Repository reuses
`StandardTags(tenant, environment)` verbatim.

### `fn.go` change — conditional Repository emission (R2, R3, R4, R6)

Two edits to the existing `RunFunction`, keeping the deferred-conversion flow
intact:

1. A new stable key constant alongside the existing ones:

   ```go
   const (
       keyBucket           resource.Name = "bucket"
       keyBucketVersioning resource.Name = "bucket-versioning"
       keyRepository       resource.Name = "repository"
   )
   ```

2. After the Bucket and BucketVersioning are added to `desired` (unchanged), read
   the enabled flag and conditionally add the Repository. `spec.repository.enabled`
   is read via `fieldpath` exactly like `spec.bucket.versioning`: absent ⇒ `false`
   (the schema default), a read error other than not-found ⇒ fatal.

   ```go
   // Resolve repository.enabled. Absent means false (the schema default) → no
   // Repository composed (R2.2, R2.3).
   repoEnabled := false
   if v, verr := paved.GetBool("spec.repository.enabled"); verr != nil {
       if !fieldpath.IsNotFound(verr) {
           response.Fatal(rsp, errors.Wrap(verr, "cannot read spec.repository.enabled from observed XR"))
           return rsp, nil
       }
   } else {
       repoEnabled = v
   }

   // Compose the ECR Repository only when enabled (R2.1). When disabled/absent
   // the key is never added to desired, so no Repository resource exists (R2.2,
   // R2.3, R4.4). tags DO apply to the Repository (unlike BucketVersioning),
   // via the same StandardTags set (R4.1–R4.3, R6.8).
   if repoEnabled {
       desired[keyRepository] = &ecrv1beta1.Repository{
           APIVersion: ptr(ecrv1beta1.RepositoryAPIVersionEcrAwsMUpboundIoV1Beta1),
           Kind:       ptr(ecrv1beta1.RepositoryKindRepository),
           Metadata: &metav1.ObjectMeta{
               Namespace: ptr(names.Namespace),
               Annotations: ptr(map[string]string{
                   externalNameAnnotation: names.RepositoryName,
               }),
           },
           Spec: &ecrv1beta1.RepositorySpec{
               ForProvider: &ecrv1beta1.RepositorySpecForProvider{
                   Region: ptr(region),
                   Tags:   ptr(tags),
               },
           },
       }
   }
   ```

Resource shape and the invariants it encodes:

- `apiVersion: ecr.aws.m.upbound.io/v1beta1`, `kind: Repository` (R2.4, R6.6) —
  carried by the model's typed `APIVersion`/`Kind` constants, so the legacy
  cluster-scoped group cannot be selected by accident.
- `metadata.namespace: <tenant>-<env>` (R2.5) — same namespace as the Bucket and
  the XR (`names.Namespace`).
- `metadata.annotations["crossplane.io/external-name"] = <tenant>-<env>-ecr`
  (R2.8, R3.3). **`metadata.name` is never set** to the external name or any
  tenant/env-derived value (R2.8, R3.4); it is left to the composition machinery,
  exactly as for the Bucket.
- `spec.forProvider.region = spec.region` (R2.6) — the same resolved `region`
  used for the Bucket (XRD default `ap-southeast-1` when absent).
- `spec.forProvider.tags = {tenant, environment, managed-by}` (R4.1–R4.3, R6.8)
  via the existing `StandardTags(tenant, environment)`. Note the key difference
  from `BucketVersioning`, which carries no tags field: the `Repository` model
  **does** expose `spec.forProvider.tags`, so the standard tag set is applied
  here.
- No `EncryptionConfiguration`, `ImageScanningConfiguration`, `ImageTagMutability`,
  or any other `forProvider` field is set — only `Region` and `Tags` — keeping
  the slice free of IAM/KMS/scanning/lifecycle/policy scope (R6.7).

The Bucket and BucketVersioning assembly, the R4.8 versioning guard, and the
deferred `toDesiredComposed` conversion are all **unchanged** (R6.1, R6.2). No
DynamoDB `Table` is ever added under any key (R6.3). The result is the exact
keyset invariant: two resources when disabled/absent, three when enabled
(R6.4, R6.5).

### `fn.go` change — status population (R5)

`status.repositoryUrl` differs from `status.bucketName` in one essential way:
the bucket name is **derivable from naming** (`names.BucketName`), but the
repository URL is **provider-reported** — the `<account>.dkr.ecr.<region>.
amazonaws.com/<name>` form is only known after AWS provisions the repo, so it
must be read from the observed `Repository.status.atProvider.repositoryUrl`
rather than constructed.

`populateStatus` is extended (still a single call from `RunFunction`, still
initialising the desired composite from the observed one so untouched status
fields are preserved). It keeps the existing `status.bucketName` behavior
unchanged and adds a parallel path for `status.repositoryUrl`. The signature
gains the enabled flag so the disabled case is handled without inspecting
observed resources:

```go
func (f *Function) populateStatus(req *fnv1.RunFunctionRequest, rsp *fnv1.RunFunctionResponse, names Names, repoEnabled bool) error {
    observed, err := request.GetObservedComposedResources(req)
    if err != nil {
        return errors.Wrap(err, "cannot get observed composed resources")
    }

    dxr, err := request.GetDesiredCompositeResource(req)
    if err != nil {
        return errors.Wrap(err, "cannot get desired composite resource")
    }
    paved := fieldpath.Pave(dxr.Resource.Object)

    // --- status.bucketName (unchanged S3 behavior, R5-of-S3) ---
    if bucket, ok := observed[keyBucket]; ok &&
        bucket.Resource.GetCondition(xpv2.TypeReady).Status == corev1.ConditionTrue {
        if err := paved.SetString("status.bucketName", names.BucketName); err != nil {
            return errors.Wrap(err, "cannot set status.bucketName on desired composite resource")
        }
    }

    // --- status.repositoryUrl (this slice) ---
    // Set ONLY when the repository is enabled AND the observed Repository is
    // Ready AND its atProvider.repositoryUrl is present and non-whitespace.
    // Otherwise the field is left absent. Derived fresh every reconcile from the
    // current observed state — never latched — so enabled→disabled or
    // ready→not-ready naturally clears it (R5.2, R5.3, R5.5, R5.7).
    if repoEnabled {
        if repo, ok := observed[keyRepository]; ok &&
            repo.Resource.GetCondition(xpv2.TypeReady).Status == corev1.ConditionTrue {
            url, uerr := fieldpath.Pave(repo.Resource.Object).GetString("status.atProvider.repositoryUrl")
            if uerr != nil && !fieldpath.IsNotFound(uerr) {
                return errors.Wrap(uerr, "cannot read status.atProvider.repositoryUrl from observed repository")
            }
            if strings.TrimSpace(url) != "" {
                if err := paved.SetString("status.repositoryUrl", url); err != nil {
                    return errors.Wrap(err, "cannot set status.repositoryUrl on desired composite resource")
                }
            }
        }
    }

    return response.SetDesiredCompositeResource(rsp, dxr)
}
```

Status contract encoded here:

- Repository Ready with a non-whitespace observed URL ⇒ `status.repositoryUrl`
  equals that observed value **byte-for-byte** (R5.1, R5.4).
- Repository not Ready, or not enabled ⇒ field left absent (R5.2, R5.5).
- Ready but observed URL missing or whitespace-only ⇒ treated as no value, field
  left absent (R5.6).
- The value is always recomputed from the current observed state; nothing is
  latched, so ready→not-ready clears it (R5.3, R5.7).

Because the disabled branch never reads the observed Repository, a stale observed
Repository from a prior enabled reconcile cannot leak into status once disabled.
`status.tableName` remains unset in this slice (DynamoDB is out of scope).

> Adding `strings` to `fn.go`'s imports is the only new import in the file;
> `naming.go` already imports `strings` and gains no new imports.

## Data Models

### TenantEnvironment (input) — already defined

The XRD (`definition.yaml`) is the source of truth and is unchanged by this
slice. Fields read by the function:

| Field | Type | Default | Used for |
| ----- | ---- | ------- | -------- |
| `spec.tenant` | string (immutable, pattern) | required | name derivation, tags |
| `spec.environment` | enum `dev`/`staging`/`prod` (immutable) | required | name derivation, tags |
| `spec.region` | enum allow-list | `ap-southeast-1` | `Bucket`/`Repository` `forProvider.region` |
| `spec.bucket.versioning` | bool | `true` | versioning status mapping (S3) |
| `spec.repository.enabled` | bool | `false` | **gates the ECR Repository (this slice)** |

`spec.table.*` exists in the schema but is **ignored** by this slice (R6.3).

Status contract written by the function:

| Field | Type | This slice |
| ----- | ---- | ---------- |
| `status.bucketName` | string | set to external name when bucket Ready (unchanged) |
| `status.repositoryUrl` | string | **set to observed `atProvider.repositoryUrl` when repo enabled + Ready + non-empty** |
| `status.tableName` | string | left unset (DynamoDB out of scope) |

### Generated provider model (output)

From `dev.crossplane.io/models/io/upbound/m/aws/ecr/v1beta1`:

- **`Repository`** — top-level typed `APIVersion *RepositoryAPIVersion` and
  `Kind *RepositoryKind` (constants
  `RepositoryAPIVersionEcrAwsMUpboundIoV1Beta1` = `ecr.aws.m.upbound.io/v1beta1`
  and `RepositoryKindRepository` = `Repository`), `Metadata *metav1.ObjectMeta`,
  `Spec *RepositorySpec`, `Status *RepositoryStatus`.
- **`RepositorySpec.ForProvider` (`*RepositorySpecForProvider`)** — the fields
  this slice sets are `Region *string` and `Tags *map[string]string`. All other
  fields (`EncryptionConfiguration`, `ImageScanningConfiguration`,
  `ImageTagMutability`, `ForceDelete`, …) are left nil (R6.7).
- **`RepositoryStatus.AtProvider` (`*RepositoryStatusAtProvider`)** — read (from
  the *observed* resource, via fieldpath) for `RepositoryURL *string`
  (json `repositoryUrl`), the provider-reported repository URL. This is the value
  mirrored into `status.repositoryUrl`.

The AWS repository name comes from the `crossplane.io/external-name` annotation,
not a `forProvider` field. These models are consumed as typed Go values and never
hand-edited (R1.3).

### Internal model — `Names`

The pure `Names` struct (above) is the only bespoke data model. It gains a
`RepositoryName` field, computed deterministically from `tenant` and
`environment` alongside `Namespace` and `BucketName`.

## Correctness Properties

*A property is a characteristic or behavior that should hold true across all valid executions of a system — a formal statement about what the system should do. Such properties serve as the bridge between human-readable specifications and machine-verifiable correctness guarantees.*

This slice suits property-based testing for the same reason the S3 slice did: the
function's core is **pure logic over a large input space** — the derived ECR
name, the conditional keyset, the fixed tag set, the group/Kind selection, and
the status-from-observed-state derivation. These vary meaningfully with input and
are cheap to run for hundreds of iterations. Provider reconcile and the
`crossplane composition render` toolchain are **not** property-tested — they are
external behavior covered by integration and CI gates (see Testing Strategy).

Generators produce valid `TenantEnvironment` inputs (tenant matching the XRD
pattern `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$`, environment from
`dev`/`staging`/`prod`, region from the allow-list, and free `bucket.versioning`,
`table.enabled`, `repository.enabled` booleans — including a branch where
`repository.enabled` is *absent* to exercise the default). For status, the
generator additionally produces a mocked observed `Repository` with a Ready flag
and an `atProvider.repositoryUrl` drawn from {well-formed URL, empty,
whitespace-only, absent}.

### Property 1: Exact resource keyset — two when disabled, three when enabled

*For any* valid `TenantEnvironment` input: when `spec.repository.enabled` is
`false` or absent, `RunFunction` emits exactly the two desired resources keyed
`bucket` and `bucket-versioning`; when `spec.repository.enabled` is `true`, it
emits exactly the three keyed `bucket`, `bucket-versioning`, and `repository`,
and no other key — regardless of `spec.table.enabled`.

*Verification:* build a valid XR (varying all flags, including an absent
repository flag), run `RunFunction`, read the response's desired composed
resources, and assert the key set equals `{bucket, bucket-versioning}` or
`{bucket, bucket-versioning, repository}` per the flag. No `Table` key ever
appears. Mirrors the existing `fn_exactly_two_property_test.go` harness,
generalised over the enabled flag.

**Validates: Requirements 2.1, 2.2, 2.3, 6.1, 6.2, 6.3, 6.4, 6.5**

_Property identifier: P-ECR-1_

### Property 2: Repository group and Kind are correct

*For any* valid input with `spec.repository.enabled` `true`, the composed
`repository` resource has `apiVersion: ecr.aws.m.upbound.io/v1beta1` and
`Kind: Repository`, never the legacy cluster-scoped group `ecr.aws.upbound.io`.

*Verification:* when enabled, read the `repository` entry's `apiVersion`/`kind`
from the desired output and assert exact equality; assert the apiVersion is not
`ecr.aws.upbound.io/...`.

**Validates: Requirements 2.4, 6.6**

_Property identifier: P-ECR-2_

### Property 3: Repository external-name equals `<tenant>-<env>-ecr`

*For any* valid input with `spec.repository.enabled` `true`, the composed
`repository` carries a `crossplane.io/external-name` annotation exactly equal to
`<tenant>-<env>-ecr`, its `spec.forProvider.region` equals `spec.region`, and its
`metadata.name` is neither that external name nor any other value derived from
tenant/environment.

*Verification:* when enabled, read the `repository` annotation and assert it
equals `tenant + "-" + environment + "-ecr"`; assert `metadata.name` is unset (or
at least not equal to the external name nor `<tenant>-<env>`); assert
`spec.forProvider.region` equals the drawn region.

**Validates: Requirements 2.6, 2.8, 3.1, 3.3, 3.4**

_Property identifier: P-ECR-3_

### Property 4: Repository namespace equals `<tenant>-<env>`

*For any* valid input with `spec.repository.enabled` `true`, the composed
`repository` lands in the namespace `<tenant>-<env>`, the same namespace as the
Bucket.

*Verification:* when enabled, read `repository.metadata.namespace` and assert it
equals `tenant + "-" + environment` and matches the bucket's namespace.

**Validates: Requirements 2.5**

_Property identifier: P-ECR-4_

### Property 5: `status.repositoryUrl` mirrors observed atProvider value only when Ready

*For any* valid input paired with a mocked observed `Repository`: when
`spec.repository.enabled` is `true` **and** the observed Repository reports Ready
**and** carries a non-whitespace `status.atProvider.repositoryUrl`,
`status.repositoryUrl` equals that observed value byte-for-byte; otherwise
(`enabled` false, not Ready, or missing/whitespace-only observed value)
`status.repositoryUrl` is absent. The value is re-derived from the current
observed state every reconcile and never latched.

*Verification:* mirror `fn_status_readiness_property_test.go`. Build a request
whose observed composed resources include a mocked `repository` with a Ready
condition (`True`/`False`) and an `atProvider.repositoryUrl` drawn from
{well-formed, empty, whitespace, absent}; run `RunFunction`; read
`status.repositoryUrl` off the desired composite. Assert exact equality in the
"enabled + Ready + non-whitespace" case, and absence in every other case.

**Validates: Requirements 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7**

_Property identifier: P-ECR-5_

### Property 6: Repository tag set is exactly the three standard tags

*For any* valid input with `spec.repository.enabled` `true`, the composed
`repository` `spec.forProvider.tags` has exactly the keys `tenant`,
`environment`, and `managed-by`, with `tenant`/`environment` equal to the source
spec fields and `managed-by` the fixed literal `crossplane`; when disabled, no
`repository` (and therefore no such tags) exists.

*Verification:* when enabled, read the `repository` tags map, assert the sorted
key set equals `{environment, managed-by, tenant}`, and assert the values; when
disabled, assert the `repository` key is absent (folds into Property 1).

**Validates: Requirements 4.1, 4.2, 4.3, 4.4, 6.8**

_Property identifier: P-ECR-6_

### Property 7: Invalid spec emits zero resources

*For any* `TenantEnvironment` XR whose required spec fields are missing or
malformed such that `RepositoryName` cannot be derived — spec absent, tenant/
environment key missing, or tenant/environment empty/whitespace, or the observed
composite unreadable — `RunFunction` reports a fatal result and emits zero
desired managed resources.

*Verification:* reuse the existing `fn_invalid_spec_property_test.go` harness
unchanged — the fatal-path invariant is not specific to ECR (BuildNames rejecting
empty inputs already gates all three names, and the deferred block emits nothing
on a fatal result). Assert the response carries a `SEVERITY_FATAL` result and the
desired composed resource map is empty.

**Validates: Requirements 3.5, 7.1, 7.5**

_Property identifier: P-ECR-7_

### Property 8: Naming is deterministic and rejects empty inputs

*For any* valid `spec.tenant` and `spec.environment` pair, `BuildNames` produces a
byte-identical `RepositoryName` on repeated invocations; if either input is empty
or whitespace-only, `BuildNames` returns an error and produces no name
(`RepositoryName == ""`), identifying the missing field.

*Verification:* exercise `naming.go` directly (no Crossplane imports). Draw a
valid tenant/environment, call `BuildNames` twice, assert `RepositoryName` is
identical and equals `<tenant>-<env>-ecr`; draw empty/whitespace inputs and
assert an error with a zero-value `Names`.

**Validates: Requirements 3.1, 3.2, 3.5**

_Property identifier: P-ECR-8_

## Error Handling

All user-facing problems surface on the XR so tenants can diagnose failures from
`kubectl describe`/events rather than reading the function pod log (R7.2).

- **Fatal, unrecoverable input problems** (R7.1, R7.5): the observed XR cannot be
  read, a required field (`tenant`, `environment`) is missing or fails
  validation, `BuildNames` returns an error, or `spec.repository.enabled` cannot
  be read for a reason other than not-found. The function calls
  `response.Fatal(rsp, err)` and returns **without** emitting any desired
  resources — the deferred conversion short-circuits on a fatal result, so a bad
  input yields exactly zero managed resources (R7.1, Property 7). `response.Fatal`
  attaches the result to the composite so it appears in the XR's events (R7.2).
- **Non-fatal repository conditions** (R7.4): reported via
  `response.Warning(rsp, ...)` with a message identifying the affected resource by
  its composition-resource key `repository`, after which the function continues
  emitting the remaining desired resources unchanged. (In this PoC the repository
  is composed unconditionally-once-enabled with no per-repository validation, so
  this path is defensive — reserved for future repository-specific warnings.)
- **Error wrapping** (R7.3): internal errors are wrapped with
  `github.com/crossplane/crossplane-runtime/v2/pkg/errors` (e.g.
  `errors.Wrap(err, "cannot read spec.repository.enabled from observed XR")` and
  `errors.Wrap(err, "cannot read status.atProvider.repositoryUrl from observed repository")`)
  before being passed to `response.Fatal`/`response.Warning`, preserving the
  originating error text.

Absent-but-optional reads are **not** errors: a missing `spec.repository.enabled`
is `fieldpath.IsNotFound` and defaults to `false`; a missing observed Repository
or a missing/whitespace `atProvider.repositoryUrl` simply leaves
`status.repositoryUrl` unset. Only unexpected read failures are surfaced.

Because the function refuses to emit a Repository with a malformed external name
(the shared `BuildNames` gate), a bad tenant/environment never reaches AWS as a
half-built resource. (A name that is invalid AWS-side despite passing the schema
is a provider condition, not a function bug, and is out of this handling's
scope.)

## Testing Strategy

A dual approach: property-based tests for the pure logic, example-based unit
tests for specific wiring and edge cases, and `crossplane composition render` as
the integration gate.

### Property-based tests (the function's pure logic)

- Library: **`pgregory.net/rapid`**, as used by the existing property tests.
  Property tests are **not** hand-rolled.
- Each of Properties P-ECR-1..P-ECR-8 is implemented as a **single**
  property-based test, in its own sibling file consistent with the existing
  `fn_*_property_test.go` / `naming_*_property_test.go` naming:

  | Property | Test file | Exercises |
  | -------- | --------- | --------- |
  | P-ECR-1 | `fn_ecr_keyset_property_test.go` | `RunFunction` desired keyset (2 vs 3) |
  | P-ECR-2 | `fn_ecr_group_kind_property_test.go` | `repository` apiVersion/Kind |
  | P-ECR-3 | `fn_ecr_extname_property_test.go` | `repository` external-name, region, no metadata.name |
  | P-ECR-4 | `fn_ecr_namespace_property_test.go` | `repository` namespace |
  | P-ECR-5 | `fn_ecr_status_repourl_property_test.go` | `status.repositoryUrl` from observed atProvider |
  | P-ECR-6 | `fn_ecr_tags_property_test.go` | `repository` tag set |
  | P-ECR-7 | `fn_ecr_invalid_spec_property_test.go` | fatal + zero resources (or reuse existing P11 harness) |
  | P-ECR-8 | `naming_ecr_repository_property_test.go` | `RepositoryName` determinism + rejection |

- Each property test runs a **minimum of 100 iterations** (rapid's default check
  count exceeds this).
- Each test is tagged with a comment referencing its design property, format:
  `// Feature: tenant-environment-ecr, Property {n}: {property text}`.
- Generators reuse the existing tenant/environment/region generators and vary
  `repository.enabled` (true/false/absent) across the input space. P-ECR-5's
  generator additionally produces a mocked observed `Repository` with a Ready flag
  and an `atProvider.repositoryUrl` from {well-formed, empty, whitespace, absent}.
- P-ECR-8 exercises `naming.go` directly (no Crossplane imports); P-ECR-1..P-ECR-7
  exercise `RunFunction` and inspect the returned desired resources / status.
- Comparisons use `github.com/google/go-cmp/cmp` where structural equality is
  asserted (e.g. the tag key set).

### Unit tests (examples, edge cases, wiring)

Table-driven (`map[string]struct{...}` + `t.Run`), per steering conventions:

- **`naming_test.go`**: extend `TestBuildNames` so the expected `Names` includes
  `RepositoryName` for each case (shortest/longest tenant, each environment) —
  satisfying R8.3's "at least one table-driven test exercising the naming
  module's Repository_External_Name derivation". `StandardTags` is already
  covered; the Repository reuses it.
- **`fn_test.go`**: extend the example-XR expectations:
  - `acme-dev` (repository disabled) ⇒ desired keyset `{bucket, bucket-versioning}`,
    no `repository`, `status.repositoryUrl` absent.
  - `globex-prod` (repository enabled) ⇒ desired keyset
    `{bucket, bucket-versioning, repository}`; the `repository` carries external
    name `globex-prod-ecr`, namespace `globex-prod`, region `ap-southeast-1`, and
    the three standard tags.
  - Error wiring (R7.2): a malformed XR yields a `Fatal` result on the composite
    and zero desired resources (existing case, unchanged).
  - Warning wiring (R7.4): if a `response.Warning` path is added, a crafted case
    asserts the warning mentions `repository` and the other resources are still
    emitted.

### Integration gate — composition render (R8.4, R8.6, R8.7)

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

- `acme-dev.yaml` (repository disabled) must render exactly the Bucket +
  BucketVersioning, no Repository.
- `globex-prod.yaml` (repository enabled) must render the Bucket +
  BucketVersioning + a `Repository` carrying `crossplane.io/external-name:
  globex-prod-ecr` (R8.7).

Both must exit `0` and emit no error; a failure names the offending example and
fails the definition of done (R8.6). This is 1–2 representative examples by
design — render exercises external CLI/provider machinery, so more iterations add
no value (the input-varying logic is already covered by the property tests).

### CI gates (R8.1, R8.2, R8.3, R8.5)

Run in the PR job (never pushing):

```bash
cd functions/compose-tenant-environment && gofmt -l . && go vet ./... && go test ./...
```

- `gofmt -l .` must print nothing (R8.1).
- `go vet ./...` must be clean (R8.2).
- `go test ./...` must pass, including the new ECR property tests and the extended
  `naming_test.go` (R8.3).
- `naming.go` importing zero Crossplane packages is a structural guarantee of the
  file split (R8.5); the new `RepositoryName` line adds only string concatenation,
  no imports. The build fails if a Crossplane import leaks in, reinforced in
  review.

### Why some criteria are not property-tested

Requirement 1 (dependency add, model generation, cache) and Requirement 8's
tooling gates are **SMOKE/INTEGRATION** concerns — one-shot checks of external
tools with no meaningful input variation — verified by the presence of the
dependency entry, a function that compiles against the generated ECR model, and
the CI commands above. Requirement 6.7 (scope exclusions) is verified structurally
(the Repository sets only `region` + `tags`) plus render-output inspection.
Requirements 7.2, 7.3, and 7.4 (report attachment, error-wrapping convention,
warning path) are verified by unit tests and review rather than universal
properties.
