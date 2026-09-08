# Implementation Plan: tenant-environment-ecr

## Overview

This slice adds optional ECR `Repository` support to the already-shipped S3-only
`TenantEnvironment` composition function. It is an additive vertical slice: no
restructuring of `RunFunction`, no changes to the XRD, the composition pipeline,
or the S3 behavior. The generated ECR models already exist under
`schemas/go/io/upbound/m/aws/ecr/v1beta1` (`Repository`), so no generator is
re-run and no generated file is hand-edited.

The tasks build strictly incrementally:

1. Declare the `provider-aws-ecr` dependency (the models are already generated).
2. Extend `naming.go` with `RepositoryName`, then its table + property tests.
3. Extend `fn.go` to conditionally emit the typed `Repository`, then its
   property tests (P-ECR-1..P-ECR-4, P-ECR-6, P-ECR-7).
4. Extend `populateStatus` to mirror the observed `repositoryUrl`, then its
   property test (P-ECR-5).
5. Verify the example wiring (`acme-dev` disabled, `globex-prod` enabled) and
   extend `fn_test.go` example expectations.
6. Run the full verification gate: `gofmt -l`, `go vet ./...`, `go test ./...`,
   and `crossplane composition render` over both examples.

Language: **Go** (as used by the existing function and the design). No language
selection needed — the design is concrete Go, not pseudocode.

## Tasks

- [x] 1. Declare the ECR provider dependency
  - Add a `type: xpkg` / `kind: Provider` dependency entry to
    `spec.dependencies` in `crossplane-project.yaml` referencing
    `xpkg.crossplane.io/crossplane-contrib/provider-aws-ecr` with
    `version: '>=v0.0.0'`, alongside the existing `provider-aws-s3` and
    `function-auto-ready` entries.
  - Do NOT hand-edit any file under `schemas/go` — the `Repository` model is
    already generated at `schemas/go/io/upbound/m/aws/ecr/v1beta1/repository.go`;
    this task only records the dependency the build/render resolves.
  - Do NOT re-run `crossplane xrd generate` or `crossplane composition generate`.
  - _Requirements: 1.1, 1.2, 1.3, 1.4_

- [x] 2. Extend the naming module with the ECR external name
  - [x] 2.1 Add `RepositoryName` to `naming.go`
    - Add a `RepositoryName string` field to the `Names` struct with the
      `"<tenant>-<env>-ecr"` comment.
    - In `BuildNames`, after the existing empty/whitespace validation, set
      `RepositoryName: namespace + "-ecr"` in the returned `Names` (reusing the
      already-computed `namespace`), so it is produced by the same validation
      gate and the same deterministic concatenation as `BucketName`.
    - Add no new imports; `naming.go` must keep zero Crossplane imports.
    - _Requirements: 3.1, 3.2, 3.5, 8.5_

  - [x] 2.2 Extend `naming_test.go` table cases for `RepositoryName`
    - In `TestBuildNames`, add the expected `RepositoryName` to every existing
      success case (`shortest tenant, dev`; `longest tenant, prod`; each
      environment), e.g. `abc-dev-ecr`, `a12345678901234567890b-prod-ecr`,
      `acme-staging-ecr`.
    - Leave the error cases asserting the zero-value `Names` (so
      `RepositoryName == ""`).
    - This satisfies R8.3's "at least one table-driven test exercising the
      naming module's Repository_External_Name derivation".
    - _Requirements: 3.1, 3.2, 8.3_

  - [x] 2.3 Write property test P-ECR-8 for `RepositoryName`
    - Create `naming_ecr_repository_property_test.go` exercising `naming.go`
      directly (no Crossplane imports), using `pgregory.net/rapid` consistent
      with the existing `naming_*_property_test.go` files.
    - **Property P-ECR-8: Naming is deterministic and rejects empty inputs**
    - Draw a valid tenant/environment, call `BuildNames` twice, assert
      `RepositoryName` is byte-identical and equals `<tenant>-<env>-ecr`; draw
      empty/whitespace inputs and assert an error with a zero-value `Names`
      (`RepositoryName == ""`).
    - Tag: `// Feature: tenant-environment-ecr, Property 8: ...`.
    - **Validates: Requirements 3.1, 3.2, 3.5**

- [x] 3. Conditionally emit the ECR Repository in `fn.go`
  - [x] 3.1 Add the `keyRepository` constant and the ECR model import
    - Add `keyRepository resource.Name = "repository"` to the existing
      composition-key `const` block in `fn.go`.
    - Add the import
      `ecrv1beta1 "dev.crossplane.io/models/io/upbound/m/aws/ecr/v1beta1"`
      alongside the existing `s3v1beta1` import.
    - _Requirements: 2.7, 6.6_

  - [x] 3.2 Read `spec.repository.enabled` and conditionally build the Repository
    - After the BucketVersioning is added to `desired` (and before
      `populateStatus`), read `spec.repository.enabled` via
      `paved.GetBool("spec.repository.enabled")`: `fieldpath.IsNotFound` ⇒
      `false` (schema default); any other read error ⇒
      `response.Fatal(rsp, errors.Wrap(...))` and return.
    - When enabled, add `desired[keyRepository] = &ecrv1beta1.Repository{...}`
      with typed `APIVersion`/`Kind` constants
      (`RepositoryAPIVersionEcrAwsMUpboundIoV1Beta1`, `RepositoryKindRepository`),
      `Metadata.Namespace = names.Namespace`,
      `Annotations = {externalNameAnnotation: names.RepositoryName}` (never
      `metadata.name`), and
      `Spec.ForProvider = { Region: region, Tags: StandardTags(...) }` reusing
      the already-computed `region` and `tags`.
    - Set no other `forProvider` field (no `EncryptionConfiguration`,
      `ImageScanningConfiguration`, `ImageTagMutability`, `ForceDelete`, etc.).
    - When disabled/absent, do not touch `desired` at all (no `repository` key).
    - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.8, 3.3, 3.4, 4.1, 4.2, 4.3, 4.4, 6.4, 6.5, 6.6, 6.7, 6.8, 7.1, 7.3_

  - [x] 3.3 Write property test P-ECR-1 for the exact keyset
    - Create `fn_ecr_keyset_property_test.go` (rapid), varying
      `repository.enabled` across true/false/absent and `table.enabled` freely.
    - **Property P-ECR-1: Resource count is exactly two when disabled, exactly
      three when enabled**
    - Assert the desired keyset equals `{bucket, bucket-versioning}` when
      disabled/absent and `{bucket, bucket-versioning, repository}` when enabled;
      no `Table` or other key ever appears. Mirror the
      `fn_exactly_two_property_test.go` harness generalised over the flag.
    - Tag: `// Feature: tenant-environment-ecr, Property 1: ...`.
    - **Validates: Requirements 2.1, 2.2, 2.3, 6.1, 6.2, 6.3, 6.4, 6.5**

  - [x] 3.4 Write property test P-ECR-2 for the Repository group and Kind
    - Create `fn_ecr_group_kind_property_test.go` (rapid), enabled inputs.
    - **Property P-ECR-2: Repository group and Kind are correct**
    - Assert the `repository` entry's `apiVersion == ecr.aws.m.upbound.io/v1beta1`
      and `kind == Repository`, and that the apiVersion is never
      `ecr.aws.upbound.io/...`.
    - Tag: `// Feature: tenant-environment-ecr, Property 2: ...`.
    - **Validates: Requirements 2.4, 6.6**

  - [x] 3.5 Write property test P-ECR-3 for external-name, region, and no metadata.name
    - Create `fn_ecr_extname_property_test.go` (rapid), enabled inputs.
    - **Property P-ECR-3: Repository external-name equals `<tenant>-<env>-ecr`**
    - Assert the `crossplane.io/external-name` annotation equals
      `tenant + "-" + environment + "-ecr"`, `spec.forProvider.region` equals the
      drawn region, and `metadata.name` is unset (or at least not the external
      name nor `<tenant>-<env>`).
    - Tag: `// Feature: tenant-environment-ecr, Property 3: ...`.
    - **Validates: Requirements 2.6, 2.8, 3.1, 3.3, 3.4**

  - [x] 3.6 Write property test P-ECR-4 for the Repository namespace
    - Create `fn_ecr_namespace_property_test.go` (rapid), enabled inputs.
    - **Property P-ECR-4: Repository namespace equals `<tenant>-<env>`**
    - Assert `repository.metadata.namespace` equals `tenant + "-" + environment`
      and matches the bucket's namespace.
    - Tag: `// Feature: tenant-environment-ecr, Property 4: ...`.
    - **Validates: Requirements 2.5**

  - [x] 3.7 Write property test P-ECR-6 for the Repository tag set
    - Create `fn_ecr_tags_property_test.go` (rapid), varying the enabled flag.
    - **Property P-ECR-6: Repository tag set is exactly the three standard tags**
    - When enabled, assert `spec.forProvider.tags` has exactly keys
      `{environment, managed-by, tenant}` (compare with `go-cmp`) with `tenant`/
      `environment` equal to the spec fields and `managed-by == crossplane`; when
      disabled, assert the `repository` key is absent.
    - Tag: `// Feature: tenant-environment-ecr, Property 6: ...`.
    - **Validates: Requirements 4.1, 4.2, 4.3, 4.4, 6.8**

  - [x] 3.8 Write property test P-ECR-7 for invalid-spec fatal behavior
    - Create `fn_ecr_invalid_spec_property_test.go` (rapid), or reuse the
      existing invalid-spec harness, generating XRs whose `tenant`/`environment`
      is missing/empty/whitespace or whose observed composite is unreadable.
    - **Property P-ECR-7: Invalid spec emits zero resources**
    - Assert the response carries a `SEVERITY_FATAL` result and the desired
      composed resource map is empty.
    - Tag: `// Feature: tenant-environment-ecr, Property 7: ...`.
    - **Validates: Requirements 3.5, 7.1, 7.5**

- [x] 4. Checkpoint - naming + emission
  - Ensure all tests pass, ask the user if questions arise.

- [x] 5. Mirror the observed repository URL into status
  - [x] 5.1 Extend `populateStatus` with the `repoEnabled` path
    - Add a `repoEnabled bool` parameter to `populateStatus` and pass the
      resolved flag from `RunFunction` (`f.populateStatus(req, rsp, names, repoEnabled)`).
    - Restructure the body to pave the desired composite once, keep the existing
      `status.bucketName` behavior unchanged, and add: when `repoEnabled` AND the
      observed `keyRepository` reports `xpv2.TypeReady == True`, read
      `status.atProvider.repositoryUrl` via fieldpath (`fieldpath.IsNotFound` ⇒
      no value; other read error ⇒ wrapped error return); if
      `strings.TrimSpace(url) != ""`, set `status.repositoryUrl` to that value
      byte-for-byte; otherwise leave it absent.
    - Never latch: derive from current observed state each reconcile; the
      disabled branch never inspects the observed Repository.
    - Add the `strings` import to `fn.go` (the only new import in the file).
    - Leave `status.tableName` unset (DynamoDB out of scope).
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7, 7.3_

  - [x] 5.2 Write property test P-ECR-5 for `status.repositoryUrl`
    - Create `fn_ecr_status_repourl_property_test.go` (rapid), mirroring
      `fn_status_readiness_property_test.go`. The generator produces a mocked
      observed `Repository` with a Ready flag (`True`/`False`) and an
      `atProvider.repositoryUrl` drawn from {well-formed, empty, whitespace,
      absent}, plus a `repository.enabled` flag.
    - **Property P-ECR-5: Status repositoryUrl mirrors observed atProvider value
      only when Ready**
    - Assert `status.repositoryUrl` equals the observed value byte-for-byte in
      the enabled + Ready + non-whitespace case, and is absent in every other
      case (disabled, not Ready, missing/whitespace-only).
    - Tag: `// Feature: tenant-environment-ecr, Property 5: ...`.
    - **Validates: Requirements 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7**

- [x] 6. Verify and extend example wiring
  - [x] 6.1 Verify the example XRs match the slice's enabled/disabled split
    - Confirm `examples/tenantenvironments/acme-dev.yaml` keeps
      `spec.repository.enabled: false` and `examples/tenantenvironments/globex-prod.yaml`
      keeps `spec.repository.enabled: true`; adjust only if they diverge (per the
      design both are already correct).
    - _Requirements: 8.4, 8.7_

  - [x] 6.2 Extend `fn_test.go` example expectations
    - Add/extend example-XR cases: `acme-dev` (disabled) ⇒ desired keyset
      `{bucket, bucket-versioning}`, no `repository`, `status.repositoryUrl`
      absent; `globex-prod` (enabled) ⇒ keyset
      `{bucket, bucket-versioning, repository}` with the `repository` carrying
      external name `globex-prod-ecr`, namespace `globex-prod`, region
      `ap-southeast-1`, and the three standard tags.
    - Keep the existing malformed-XR case asserting `Fatal` + zero desired
      resources unchanged.
    - _Requirements: 2.1, 2.2, 2.5, 2.6, 2.8, 4.1, 6.4, 6.5, 7.1, 7.2_

- [x] 7. Verification gate
  - [x] 7.1 Run Go format, vet, and tests
    - From `functions/compose-tenant-environment`, run `gofmt -l .` (must print
      nothing), `go vet ./...` (clean), and `go test ./...` (all pass, including
      the new ECR property tests and extended `naming_test.go`/`fn_test.go`).
    - Fix any diffs/findings/failures until all three are clean.
    - _Requirements: 8.1, 8.2, 8.3_

  - [x] 7.2 Run composition render over both examples
    - Run `crossplane composition render examples/tenantenvironments/acme-dev.yaml apis/tenantenvironments/composition.yaml`
      and the same for `globex-prod.yaml`; each must exit `0` with no error
      output.
    - Confirm `acme-dev` renders only Bucket + BucketVersioning (no Repository)
      and `globex-prod` additionally renders a `Repository` carrying
      `crossplane.io/external-name: globex-prod-ecr`, and that every composed
      resource carries a non-empty external-name annotation.
    - _Requirements: 8.4, 8.6, 8.7_

- [x] 8. Final checkpoint - definition of done
  - Ensure all tests pass and both renders succeed, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional (the property/naming property tests) and can
  be skipped for a faster MVP, but P-ECR-1..P-ECR-8 are the design's stated
  correctness guards — implement them before considering the slice done.
- Each task references specific requirement clauses (and, for test tasks, the
  design property identifier) for traceability.
- The generated ECR models under `schemas/go` are tool-generated and must never
  be hand-edited; if a type were missing the fix is to re-add the dependency and
  regenerate (out of scope here — the `Repository` type is already present).
- Do not re-run `crossplane xrd generate` or `crossplane composition generate` —
  both are destructive to hand-maintained files.
- Property tests use `pgregory.net/rapid` (rapid's default check count exceeds
  100 iterations) and `github.com/google/go-cmp/cmp` for structural equality,
  consistent with the existing `fn_*_property_test.go` files.
- Composition-resource map keys (`bucket`, `bucket-versioning`, `repository`) are
  stable identifiers and are not renamed.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1", "2.1"] },
    { "id": 1, "tasks": ["2.2", "2.3", "3.1"] },
    { "id": 2, "tasks": ["3.2"] },
    { "id": 3, "tasks": ["3.3", "3.4", "3.5", "3.6", "3.7", "3.8", "5.1", "6.1"] },
    { "id": 4, "tasks": ["5.2", "6.2"] },
    { "id": 5, "tasks": ["7.1"] },
    { "id": 6, "tasks": ["7.2"] }
  ]
}
```
