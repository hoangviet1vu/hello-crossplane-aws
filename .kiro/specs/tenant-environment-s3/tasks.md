# Implementation Plan: tenant-environment-s3

## Overview

This plan implements the S3-only vertical slice of `hello-crossplane-aws`: an
embedded Go composition function that turns a `TenantEnvironment` XR into two
namespaced S3 managed resources — a `Bucket` (always) and a `BucketVersioning` —
and populates `status.bucketName` when the bucket reports ready.

The build order is strictly incremental. First confirm the provider dependency
and generated models are in place, then create the `Composition` that references
the embedded function. Next build the pure `naming.go` (no Crossplane imports)
with its tests, then layer `fn.go`: bucket, then versioning, then status, then
error handling. Property-based tests (rapid) follow the logic they exercise, and
the render/CI gates close the definition of done. Each step builds on the
previous one, ending with the two files wired into a rendering pipeline — no
orphaned code.

Language: **Go** (per steering `tech.md`; the design specifies Go directly, so no
language selection is needed).

## Tasks

- [x] 1. Confirm the S3 provider dependency and generated models
  - Verify `crossplane-project.yaml` declares `xpkg.crossplane.io/crossplane-contrib/provider-aws-s3` in the `spec.dependencies` list (already present); if absent, re-add it via `crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-s3`
  - Confirm the generated models exist under `schemas/go` (module `dev.crossplane.io/models`, package `.../io/upbound/m/aws/s3/v1beta1`) and expose the namespaced `Bucket` and `BucketVersioning` types; do NOT hand-edit generated models — if a type is missing, re-add the dependency and regenerate
  - Run `crossplane dependency update-cache` so the provider resolves from the local cache without a network fetch
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5_

- [x] 2. Create the Composition pipeline
  - Create `apis/tenantenvironments/composition.yaml` as `apiextensions.crossplane.io/v1` `Composition`, `mode: Pipeline`, `compositeTypeRef` referencing `platform.hello-crossplane.io/v1alpha1` / `TenantEnvironment`
  - Add step `compose-tenant-environment` with `functionRef.name: hello-crossplane-awscompose-tenant-environment` (project name + function name concatenated by the CLI — not a typo), followed by step `auto-ready` with `functionRef.name: function-auto-ready`
  - This file is written once and then hand-edited; do not re-run `crossplane composition generate`
  - _Requirements: 6.3, 5.1_

- [x] 3. Scaffold the embedded function module
  - Create `functions/compose-tenant-environment/` with `go.mod` (module for the embedded function) and `go.sum`, requiring `function-sdk-go`, `github.com/crossplane/crossplane-runtime/v2`, `github.com/google/go-cmp`, `pgregory.net/rapid`, and `dev.crossplane.io/models` (the generated models)
  - Add a `replace` (or workspace) directive so `dev.crossplane.io/models` resolves to the repo-local `schemas/go`
  - _Requirements: 8.5_

- [x] 4. Implement the pure naming and mapping module
  - [x] 4.1 Implement `naming.go` with no Crossplane imports
    - `BuildNames(tenant, environment string) (Names, error)` returning `Names{Namespace: "<tenant>-<env>", BucketName: "<tenant>-<env>-bucket"}`; return an error and no names when `tenant` or `environment` is empty or whitespace-only
    - `VersioningStatus(enabled bool) string` → `"Enabled"` when true, `"Suspended"` when false
    - `StandardTags(tenant, environment string) map[string]string` → exactly the keys `tenant`, `environment`, `managed-by` and no others
    - Deterministic: identical inputs always yield byte-identical output (no time, randomness, or map-order dependence)
    - _Requirements: 3.1, 3.2, 3.6, 4.3, 4.4, 6.5, 8.5_

  - [x] 4.2 Write table-driven unit tests for `naming.go` in `naming_test.go`
    - `map[string]struct{...}` + `t.Run`, compared with `github.com/google/go-cmp/cmp`
    - Cover shortest and longest tenants allowed by the XRD pattern, each environment value, empty/whitespace inputs, both `VersioningStatus` branches, and the exact `StandardTags` key set (satisfies R8.3's "at least one table-driven test exercising the naming module")
    - _Requirements: 8.3_

  - [x] 4.3 Write property test: name derivation is deterministic
    - **Property 3: Name derivation is deterministic**
    - **Validates: Requirements 3.2**
    - rapid generator over valid tenant/environment; assert repeated `BuildNames` calls produce byte-identical `BucketName` and `Namespace`; ≥100 iterations; tag `// Feature: tenant-environment-s3, Property 3: ...`
    - _Requirements: 3.2_

  - [x] 4.4 Write property test: naming rejects empty or whitespace input
    - **Property 4: Naming rejects empty or whitespace input**
    - **Validates: Requirements 2.7, 3.6**
    - rapid generator over empty/whitespace-only tenant or environment; assert `BuildNames` returns an error and no names; ≥100 iterations; tagged comment
    - _Requirements: 2.7, 3.6_

- [ ] 5. Checkpoint - naming module compiles and its tests pass
  - Run `gofmt -l .`, `go vet ./...`, and `go test ./...` in `functions/compose-tenant-environment`; ensure all tests pass, ask the user if questions arise

- [x] 6. Implement `RunFunction` core and the Bucket resource
  - [x] 6.1 Implement the `fn.go` entrypoint and observed-XR read
    - Follow the `function-sdk-go` scaffold: implement `RunFunction`, read the observed XR via `request.GetObservedCompositeResource`, and read `spec.tenant`, `spec.environment`, `spec.region`, `spec.bucket.versioning`
    - Call `BuildNames(tenant, environment)`; collect desired resources into a `map[resource.Name]any` and convert them to SDK desired resources in a single deferred block (do not restructure into per-resource conversion)
    - _Requirements: 2.1, 2.2, 6.3_

  - [x] 6.2 Build the Bucket managed resource under map key `bucket`
    - Build `s3v1beta1.Bucket` (`apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: Bucket`) in namespace `<tenant>-<env>`, unconditionally for every XR regardless of `spec.table.enabled` / `spec.repository.enabled`
    - Set the `crossplane.io/external-name` annotation to `<tenant>-<env>-bucket`; never set `metadata.name` to the external name or any tenant/env-derived value
    - Set `spec.forProvider.region` from `spec.region` and `spec.forProvider.tags` from `StandardTags`
    - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 3.3, 3.4, 3.5, 6.4, 6.5_

  - [x] 6.3 Write property test: the bucket is always composed
    - **Property 1: The bucket is always composed**
    - **Validates: Requirements 2.1, 2.2**
    - rapid generator varying both `enabled` flags; assert the desired map always contains key `bucket`; ≥100 iterations; tagged comment
    - _Requirements: 2.1, 2.2_

  - [x] 6.4 Write property test: bucket external name format and no metadata-name derivation
    - **Property 2: Bucket external name format and no metadata name derivation**
    - **Validates: Requirements 2.6, 3.1, 3.3, 3.4**
    - assert the bucket external-name annotation equals `<tenant>-<env>-bucket` and `metadata.name` is not set to that or any tenant/env-derived value; ≥100 iterations; tagged comment
    - _Requirements: 2.6, 3.1, 3.3, 3.4_

- [x] 7. Implement the BucketVersioning resource
  - [x] 7.1 Build the BucketVersioning managed resource under map key `bucket-versioning`
    - Build `s3v1beta1.BucketVersioning` (`apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: BucketVersioning`) in namespace `<tenant>-<env>`, matching the bucket's namespace
    - Set `versioningConfiguration.status` via `VersioningStatus(spec.bucket.versioning)`; treat absent as `true` → `Enabled`; set no other value
    - Reference the bucket by its external name (`<tenant>-<env>-bucket`) via `spec.forProvider.bucket`; if the `bucket` entry is absent from the desired map, do not emit `bucket-versioning` and surface an error identifying the missing bucket
    - Set `spec.forProvider.tags` from `StandardTags`
    - _Requirements: 4.1, 4.2, 4.6, 4.7, 4.8, 4.3, 4.4, 4.5, 6.4, 6.5_

  - [x] 7.2 Write property test: versioning status maps from the spec flag
    - **Property 6: Versioning status maps from the spec flag**
    - **Validates: Requirements 4.3, 4.4, 4.5**
    - assert `versioningConfiguration.status` is `Enabled` when versioning is true or absent, `Suspended` when false, and no other value; ≥100 iterations; tagged comment
    - _Requirements: 4.3, 4.4, 4.5_

  - [x] 7.3 Write property test: namespace consistency and versioning-to-bucket reference
    - **Property 7: Namespace consistency and versioning-to-bucket reference**
    - **Validates: Requirements 2.4, 4.6, 4.7**
    - assert both resources are in namespace `<tenant>-<env>` and BucketVersioning references the bucket by external name `<tenant>-<env>-bucket`; ≥100 iterations; tagged comment
    - _Requirements: 2.4, 4.6, 4.7_

  - [x] 7.4 Write property test: every composed resource uses the namespaced S3 group and correct Kind
    - **Property 5: Every composed resource uses the namespaced S3 group and correct Kind**
    - **Validates: Requirements 2.3, 4.2, 6.4**
    - assert every emitted resource `apiVersion` is `s3.aws.m.upbound.io/v1beta1` (never `s3.aws.upbound.io`), `bucket` has Kind `Bucket`, `bucket-versioning` has Kind `BucketVersioning`; ≥100 iterations; tagged comment
    - _Requirements: 2.3, 4.2, 6.4_

  - [x] 7.5 Write property test: exactly two resources are composed
    - **Property 8: Exactly two resources are composed**
    - **Validates: Requirements 4.1, 6.1, 6.2, 6.3**
    - assert the desired-map key set is exactly `{ "bucket", "bucket-versioning" }` regardless of `spec.table.enabled` / `spec.repository.enabled` — no `Table`, no `Repository`, no other key; ≥100 iterations; tagged comment
    - _Requirements: 4.1, 6.1, 6.2, 6.3_

  - [x] 7.6 Write property test: composed resources carry exactly the standard tag set
    - **Property 9: Composed resources carry exactly the standard tag set**
    - **Validates: Requirements 6.5**
    - assert every tagged resource has exactly keys `tenant`, `environment`, `managed-by`, with `tenant`/`environment` matching the XR spec; ≥100 iterations; tagged comment
    - _Requirements: 6.5_

- [x] 8. Implement status population from observed bucket readiness
  - [x] 8.1 Populate `status.bucketName` from current bucket readiness
    - Read the observed composed `bucket` resource's `Ready` condition; when Ready, set `status.bucketName` to the bucket external name byte-for-byte (no prefix/suffix/whitespace); when not Ready, leave `status.bucketName` unset; clear it on a ready→not-ready transition (derive from current observed readiness each reconcile, do not latch)
    - Leave `status.tableName` and `status.repositoryUrl` unset in this slice
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5, 5.6_

  - [x] 8.2 Write property test: status bucket name reflects current bucket readiness
    - **Property 10: Status bucket name reflects current bucket readiness**
    - **Validates: Requirements 5.1, 5.2, 5.3, 5.4, 5.5, 5.6**
    - rapid generator with a mocked observed-bucket readiness flag; assert `status.bucketName` equals the external name when Ready, is unset when not Ready, and `status.tableName` / `status.repositoryUrl` remain unset in all cases; ≥100 iterations; tagged comment
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5, 5.6_

- [x] 9. Implement error handling through the composition response
  - [x] 9.1 Surface fatal, warning, and guard errors on the XR
    - On unreadable/invalid XR or missing/malformed `tenant`/`environment` (including a `BuildNames` error), call `response.Fatal` and return without emitting any desired resources
    - For the R4.8 guard (bucket entry absent when assembling versioning), do not emit versioning and surface an error identifying the missing bucket
    - Report non-fatal conditions via `response.Warning` (identifying the affected resource) and continue emitting remaining resources; attach all reports to the XR so they appear in its events; wrap internal errors with `github.com/crossplane/crossplane-runtime/v2/pkg/errors` before surfacing
    - _Requirements: 7.1, 7.2, 7.3, 7.4, 4.8_

  - [x] 9.2 Write property test: invalid spec yields a fatal result and no resources
    - **Property 11: Invalid spec yields a fatal result and no resources**
    - **Validates: Requirements 7.1**
    - rapid generator producing XRs with missing/malformed `tenant`/`environment`; assert the response carries a fatal result and zero desired resources; ≥100 iterations; tagged comment
    - _Requirements: 7.1_

  - [x] 9.3 Write table-driven unit tests for wiring and edge cases in `fn_test.go`
    - `map[string]struct{...}` + `t.Run`, compared with `go-cmp`: expected desired output for `acme-dev` (versioning defaulted/enabled, table & repo disabled) and `globex-prod` (versioning explicitly true); malformed XR → `Fatal` targeted at the composite with zero desired resources; R4.8 guard path (bucket missing → no versioning + error) driven by crafted internal state
    - _Requirements: 7.1, 7.2, 4.8_

- [ ] 10. Checkpoint - full function compiles and all Go checks pass
  - Run `gofmt -l .` (must print nothing), `go vet ./...` (clean), and `go test ./...` (all tests pass, including property and table-driven tests) in `functions/compose-tenant-environment`; ensure all tests pass, ask the user if questions arise
  - _Requirements: 8.1, 8.2, 8.3_

- [ ] 11. Verify the composition render integration gate
  - Run `crossplane composition render examples/tenantenvironments/acme-dev.yaml apis/tenantenvironments/composition.yaml` and `crossplane composition render examples/tenantenvironments/globex-prod.yaml apis/tenantenvironments/composition.yaml`; each must exit `0` and emit no error
  - If render fails for any example, treat the definition of done as not met, identify the failing example, and fix the function/composition (a failure here is a wiring or model-usage bug, not a test-only concern)
  - _Requirements: 8.4, 8.6_

## Notes

- Tasks marked with `*` are optional (unit and property tests) and can be skipped for a faster MVP; core implementation tasks are never marked optional.
- Each task references specific requirement acceptance criteria for traceability; property-test tasks additionally name the design property they implement.
- All 11 correctness properties are covered by a dedicated property-based test task (rapid, ≥100 iterations, tagged with a `// Feature: tenant-environment-s3, Property {n}: ...` comment).
- Property tests 2, 3, 4 exercise `naming.go` directly (no Crossplane imports); the rest exercise `RunFunction` and inspect returned desired resources / status.
- Checkpoints enforce the definition-of-done gates incrementally: `gofmt -l` empty, `go vet ./...` clean, `go test ./...` passing, and `crossplane composition render` succeeding for every example.
- Generated models under `schemas/go` are never hand-edited; missing types are fixed by re-adding the dependency and regenerating.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1", "2", "3"] },
    { "id": 1, "tasks": ["4.1"] },
    { "id": 2, "tasks": ["4.2", "4.3", "4.4"] },
    { "id": 3, "tasks": ["6.1"] },
    { "id": 4, "tasks": ["6.2"] },
    { "id": 5, "tasks": ["6.3", "6.4", "7.1"] },
    { "id": 6, "tasks": ["7.2", "7.3", "7.4", "7.5", "7.6", "8.1"] },
    { "id": 7, "tasks": ["8.2", "9.1"] },
    { "id": 8, "tasks": ["9.2", "9.3"] },
    { "id": 9, "tasks": ["11"] }
  ]
}
```
