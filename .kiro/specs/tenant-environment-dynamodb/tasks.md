# Implementation Plan: tenant-environment-dynamodb

## Overview

This slice adds optional DynamoDB `Table` support to the already-shipped
S3 + ECR `TenantEnvironment` composition function, completing the product's
resource trio. It is an additive vertical slice: no restructuring of
`RunFunction`, no changes to the XRD, the composition pipeline, or the S3/ECR
behavior. The `spec.table.enabled` / `spec.table.hashKey` /
`spec.table.billingMode` fields and the `status.tableName` field already exist
in the hand-maintained XRD; this slice implements the composition behind them.

The one difference from the ECR slice: the DynamoDB provider models are **not
yet generated**. Task 1 therefore does real work — declaring the
`provider-aws-dynamodb` dependency AND running `crossplane dependency add` +
`crossplane dependency update-cache` to generate the typed `Table` model under
`schemas/go/io/upbound/m/aws/dynamodb/v1beta1`. The `fn.go` import task depends
on that generation completing.

The tasks build strictly incrementally:

1. Declare the `provider-aws-dynamodb` dependency and generate its Go models.
2. Extend `naming.go` with `TableName`, then its table + property tests.
3. Extend `fn.go` to conditionally emit the typed `Table`, then its property
   tests (P-DDB-1..P-DDB-6, P-DDB-8, P-DDB-9, P-DDB-11).
4. Extend `populateStatus` to mirror the derived `tableName` when Ready, then
   its property test (P-DDB-7).
5. Verify the example wiring (`acme-dev` disabled, `globex-prod` enabled) and
   extend `fn_test.go` example expectations, including a `PROVISIONED` case.
6. Run the full verification gate: `gofmt -l`, `go vet ./...`, `go test ./...`,
   and `crossplane composition render` over both examples.

Language: **Go** (as used by the existing function and the design). No language
selection needed — the design is concrete Go, not pseudocode.

## Tasks

- [x] 1. Declare the DynamoDB provider dependency and generate its models
  - Run `crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-dynamodb`
    from the project root, which adds a `type: xpkg` / `kind: Provider`
    dependency entry to `spec.dependencies` in `crossplane-project.yaml`
    (`version: '>=v0.0.0'`, alongside the existing `provider-aws-s3`,
    `provider-aws-ecr`, and `function-auto-ready` entries) AND generates the
    typed Go models under `schemas/go/io/upbound/m/aws/dynamodb/v1beta1`,
    including the namespaced `Table` type.
  - Run `crossplane dependency update-cache` so the provider resolves from the
    local cache without a network fetch for a subsequent build/render.
  - Confirm the generated `Table` type exists at
    `schemas/go/io/upbound/m/aws/dynamodb/v1beta1`; if it is absent, re-add the
    dependency and regenerate rather than editing any file under `schemas/go`.
  - Do NOT hand-edit any generated file under `schemas/go` — these are
    tool-generated and consumed as typed Go values.
  - Do NOT re-run `crossplane xrd generate` or `crossplane composition generate`.
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6_

- [x] 2. Extend the naming module with the DynamoDB external name
  - [x] 2.1 Add `TableName` to `naming.go`
    - Add a `TableName string` field to the `Names` struct with the
      `"<tenant>-<env>-dtbl"` comment.
    - In `BuildNames`, after the existing empty/whitespace validation, set
      `TableName: namespace + "-dtbl"` in the returned `Names` (reusing the
      already-computed `namespace`), so it is produced by the same validation
      gate and the same deterministic concatenation as `BucketName` and
      `RepositoryName`.
    - Add no new imports; `naming.go` must keep zero Crossplane imports.
    - _Requirements: 3.1, 3.2, 3.5, 9.5_

  - [x] 2.2 Extend `naming_test.go` table cases for `TableName`
    - In `TestBuildNames`, add the expected `TableName` to every existing
      success case (`shortest tenant, dev`; `longest tenant, prod`; each
      environment), e.g. `abc-dev-dtbl`, `a12345678901234567890b-prod-dtbl`,
      `acme-staging-dtbl`.
    - Leave the error cases asserting the zero-value `Names` (so
      `TableName == ""`).
    - This satisfies R9.3's "at least one table-driven test exercising the
      Naming_Module's Table_External_Name derivation".
    - _Requirements: 3.1, 3.2, 9.3_

  - [x] 2.3 Write property test P-DDB-10 for `TableName`
    - Create `naming_ddb_table_property_test.go` exercising `naming.go` directly
      (no Crossplane imports), using `pgregory.net/rapid` consistent with the
      existing `naming_*_property_test.go` files (min 100 iterations).
    - **Property 10: Naming is deterministic and rejects empty inputs**
    - Draw a valid tenant/environment, call `BuildNames` twice, assert
      `TableName` is byte-identical and equals `<tenant>-<env>-dtbl`; draw
      empty/whitespace inputs and assert an error with a zero-value `Names`
      (`TableName == ""`).
    - Tag: `// Feature: tenant-environment-dynamodb, Property 10: ...`.
    - **Validates: Requirements 3.1, 3.2, 3.5**

- [x] 3. Conditionally emit the DynamoDB Table in `fn.go`
  - [x] 3.1 Add the `keyTable` constant, supporting constants, and the DynamoDB model import
    - Add `keyTable resource.Name = "table"` to the existing composition-key
      `const` block in `fn.go`.
    - Add the `defaultHashKey = "id"`, `defaultBillingMode = "PAY_PER_REQUEST"`,
      and `billingModeProvisioned = "PROVISIONED"` constants near
      `defaultRegion`.
    - Add the import
      `dynamodbv1beta1 "dev.crossplane.io/models/io/upbound/m/aws/dynamodb/v1beta1"`
      alongside the existing `s3v1beta1` / `ecrv1beta1` imports.
    - Depends on task 1 (the generated DynamoDB model must exist to import).
    - _Requirements: 2.7, 7.7_

  - [x] 3.2 Read the table spec fields and conditionally build the Table
    - After the ECR Repository block (and before `populateStatus`), read
      `spec.table.enabled` via `paved.GetBool("spec.table.enabled")`:
      `fieldpath.IsNotFound` ⇒ `false` (schema default); any other read error ⇒
      `response.Fatal(rsp, errors.Wrap(...))` and return.
    - When enabled, read `spec.table.hashKey` (default `defaultHashKey`) and
      `spec.table.billingMode` (default `defaultBillingMode`) via
      `paved.GetString`, each tolerating `fieldpath.IsNotFound` and treating any
      other read error as fatal.
    - Add `desired[keyTable] = buildTable(names, region, tags, hashKey, billingMode)`.
    - When disabled/absent, do not touch `desired` at all (no `table` key).
    - _Requirements: 2.1, 2.2, 2.3, 2.5, 2.6, 2.7, 2.8, 3.3, 3.4, 4.1, 4.2, 4.4, 4.5, 8.1_

  - [x] 3.3 Implement the `buildTable` helper
    - Add a `buildTable(names Names, region string, tags map[string]string, hashKey, billingMode string) *dynamodbv1beta1.Table`
      helper (plain values in, typed model out — no Crossplane types beyond the
      generated model).
    - Set typed `APIVersion`/`Kind` constants
      (`TableAPIVersionDynamodbAwsMUpboundIoV1Beta1`, `TableKindTable`),
      `Metadata.Namespace = names.Namespace`,
      `Annotations = {externalNameAnnotation: names.TableName}` (never
      `metadata.name`).
    - Set `Spec.ForProvider` fields: `Region = region`, `Tags = tags`,
      `HashKey = hashKey`, `BillingMode = billingMode`, and exactly one
      `Attribute` item `{Name: hashKey, Type: "S"}`.
    - Set `ReadCapacity` and `WriteCapacity` each to `1` ONLY when
      `billingMode == billingModeProvisioned`; leave both unset otherwise.
    - Set no other `forProvider` field (no `ServerSideEncryption`,
      `PointInTimeRecovery`, `StreamEnabled`, `GlobalSecondaryIndex`,
      `LocalSecondaryIndex`, `Ttl`, `ForceDestroy`, etc.).
    - Adapt to the exact tool-generated identifiers if the generator differs
      from the design's assumed shape.
    - _Requirements: 2.4, 2.8, 3.3, 3.4, 4.1, 4.3, 4.4, 4.6, 4.7, 5.1, 5.2, 5.3, 7.7, 7.8, 7.9_

  - [x] 3.4 Write property test P-DDB-1 for the exact keyset
    - Create `fn_ddb_keyset_property_test.go` (rapid, min 100 iterations),
      varying `table.enabled` across true/false/absent and `repository.enabled`
      freely.
    - **Property 1: Exact resource keyset reflects the enabled flags**
    - Assert the desired keyset equals `{bucket, bucket-versioning}` plus
      `repository` and/or `table` per the flags, and no other key. Mirror the
      `fn_ecr_keyset_property_test.go` harness generalised over `table.enabled`.
    - Tag: `// Feature: tenant-environment-dynamodb, Property 1: ...`.
    - **Validates: Requirements 2.1, 2.2, 2.3, 2.7, 5.4, 7.4, 7.5, 7.6**

  - [x] 3.5 Write property test P-DDB-2 for the Table group and Kind
    - Create `fn_ddb_group_kind_property_test.go` (rapid), enabled inputs.
    - **Property 2: Table group and Kind are correct**
    - Assert the `table` entry's `apiVersion == dynamodb.aws.m.upbound.io/v1beta1`
      and `kind == Table`, and that the apiVersion is never
      `dynamodb.aws.upbound.io/...`.
    - Tag: `// Feature: tenant-environment-dynamodb, Property 2: ...`.
    - **Validates: Requirements 2.4, 7.7**

  - [x] 3.6 Write property test P-DDB-3 for external-name, region, and no metadata.name
    - Create `fn_ddb_extname_property_test.go` (rapid), enabled inputs.
    - **Property 3: Table external-name equals `<tenant>-<env>-dtbl`**
    - Assert the `crossplane.io/external-name` annotation equals
      `tenant + "-" + environment + "-dtbl"`, `spec.forProvider.region` equals
      the drawn region, and `metadata.name` is unset (or at least not the
      external name nor `<tenant>-<env>`).
    - Tag: `// Feature: tenant-environment-dynamodb, Property 3: ...`.
    - **Validates: Requirements 2.6, 2.8, 3.1, 3.3, 3.4**

  - [x] 3.7 Write property test P-DDB-4 for the Table namespace
    - Create `fn_ddb_namespace_property_test.go` (rapid), enabled inputs.
    - **Property 4: Table namespace equals `<tenant>-<env>`**
    - Assert `table.metadata.namespace` equals `tenant + "-" + environment` and
      matches the bucket's namespace.
    - Tag: `// Feature: tenant-environment-dynamodb, Property 4: ...`.
    - **Validates: Requirements 2.5**

  - [x] 3.8 Write property test P-DDB-5 for the key schema
    - Create `fn_ddb_keyschema_property_test.go` (rapid), enabled inputs varying
      present/absent `hashKey`.
    - **Property 5: Key schema mirrors the spec**
    - Assert `spec.forProvider.hashKey` equals the drawn hash key (or `id` when
      absent); assert exactly one `attribute` item whose `name` equals the hash
      key and whose `type` is `S`.
    - Tag: `// Feature: tenant-environment-dynamodb, Property 5: ...`.
    - **Validates: Requirements 4.1, 4.2, 4.3**

  - [x] 3.9 Write property test P-DDB-6 for billing mode and capacity
    - Create `fn_ddb_billing_property_test.go` (rapid), enabled inputs varying
      present/absent `billingMode` across both enum values.
    - **Property 6: Billing mode and capacity are consistent**
    - Assert `spec.forProvider.billingMode` equals the drawn mode (or
      `PAY_PER_REQUEST` when absent); assert `readCapacity`/`writeCapacity` are
      unset for `PAY_PER_REQUEST` and both equal `1` for `PROVISIONED`.
    - Tag: `// Feature: tenant-environment-dynamodb, Property 6: ...`.
    - **Validates: Requirements 4.4, 4.5, 4.6, 4.7**

  - [x] 3.10 Write property test P-DDB-8 for the Table tag set
    - Create `fn_ddb_tags_property_test.go` (rapid), varying the enabled flag.
    - **Property 8: Table tag set is exactly the three standard tags**
    - When enabled, assert `spec.forProvider.tags` has exactly keys
      `{environment, managed-by, tenant}` (compare with `go-cmp`) with `tenant`/
      `environment` equal to the spec fields and `managed-by == crossplane`; when
      disabled, assert the `table` key is absent.
    - Tag: `// Feature: tenant-environment-dynamodb, Property 8: ...`.
    - **Validates: Requirements 5.1, 5.2, 5.3, 5.4, 7.9**

  - [x] 3.11 Write property test P-DDB-9 for invalid-spec fatal behavior
    - Create `fn_ddb_invalid_spec_property_test.go` (rapid), or reuse the
      existing invalid-spec harness, generating XRs whose `tenant`/`environment`
      is missing/empty/whitespace or whose observed composite is unreadable.
    - **Property 9: Invalid spec emits zero resources**
    - Assert the response carries a `SEVERITY_FATAL` result and the desired
      composed resource map is empty.
    - Tag: `// Feature: tenant-environment-dynamodb, Property 9: ...`.
    - **Validates: Requirements 3.5, 8.1, 8.5**

  - [x] 3.12 Write property test P-DDB-11 for S3/ECR invariance
    - Create `fn_ddb_s3_ecr_unchanged_property_test.go` (rapid).
    - **Property 11: S3 and ECR behavior is unchanged**
    - Run `RunFunction` twice on inputs differing only in `spec.table.*`
      (enabled/disabled, varying hashKey/billingMode); extract the `bucket`,
      `bucket-versioning`, and `repository` entries from each desired map and
      assert they are equal with `go-cmp` (the `table` entry is excluded).
    - Tag: `// Feature: tenant-environment-dynamodb, Property 11: ...`.
    - **Validates: Requirements 7.1, 7.2, 7.3**

- [x] 4. Checkpoint - naming + emission
  - Ensure all tests pass, ask the user if questions arise.

- [x] 5. Mirror the derived table name into status
  - [x] 5.1 Extend `populateStatus` with the `tableEnabled` path
    - Add a `tableEnabled bool` parameter to `populateStatus` and pass the
      resolved flag from `RunFunction`
      (`f.populateStatus(req, rsp, names, repoEnabled, tableEnabled)`).
    - Keep the existing `status.bucketName` and `status.repositoryUrl` behavior
      unchanged, and add: when `tableEnabled` AND the observed `keyTable` reports
      `xpv2.TypeReady == True`, set `status.tableName` to `names.TableName`
      byte-for-byte (derived, like S3 `bucketName` — NOT read from
      `atProvider`); otherwise leave it absent.
    - Never latch: derive from current observed state each reconcile; the
      disabled branch never inspects the observed Table, so a stale observed
      Table cannot leak into status once disabled.
    - _Requirements: 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 8.3_

  - [x] 5.2 Write property test P-DDB-7 for `status.tableName`
    - Create `fn_ddb_status_tablename_property_test.go` (rapid, min 100
      iterations), mirroring `fn_ecr_status_repourl_property_test.go`. The
      generator produces a mocked observed `Table` with a Ready flag
      (`True`/`False`/absent) plus a `table.enabled` flag.
    - **Property 7: `status.tableName` mirrors the external name only when Ready**
    - Assert `status.tableName` equals `names.TableName` byte-for-byte in the
      enabled + Ready case, and is absent in every other case (disabled, not
      observed, not Ready).
    - Tag: `// Feature: tenant-environment-dynamodb, Property 7: ...`.
    - **Validates: Requirements 6.1, 6.2, 6.3, 6.4, 6.5, 6.6**

- [x] 6. Verify and extend example wiring
  - [x] 6.1 Verify the example XRs match the slice's enabled/disabled split
    - Confirm `examples/tenantenvironments/acme-dev.yaml` keeps
      `spec.table.enabled: false` and `examples/tenantenvironments/globex-prod.yaml`
      keeps `spec.table.enabled: true`; adjust only if they diverge (per the
      design both are already correct).
    - _Requirements: 9.4, 9.7_

  - [x] 6.2 Extend `fn_test.go` example expectations
    - Add/extend example-XR cases: `acme-dev` (table disabled) ⇒ desired keyset
      `{bucket, bucket-versioning}`, no `table`, `status.tableName` absent;
      `globex-prod` (table enabled, repository enabled) ⇒ keyset
      `{bucket, bucket-versioning, repository, table}` with the `table` carrying
      external name `globex-prod-dtbl`, namespace `globex-prod`, region
      `ap-southeast-1`, `hashKey: id`, one attribute `{id, S}`,
      `billingMode: PAY_PER_REQUEST` with no capacities, and the three standard
      tags.
    - Add a `PROVISIONED` case (crafted inline) asserting
      `readCapacity == writeCapacity == 1`.
    - Keep the existing malformed-XR case asserting `Fatal` + zero desired
      resources unchanged.
    - _Requirements: 2.1, 2.5, 2.6, 2.8, 4.1, 4.3, 4.4, 4.5, 4.6, 4.7, 5.1, 7.4, 7.6_

- [x] 7. Verification gate
  - [x] 7.1 Run Go format, vet, and tests
    - From `functions/compose-tenant-environment`, run `gofmt -l .` (must print
      nothing), `go vet ./...` (clean), and `go test ./...` (all pass, including
      the new DynamoDB property tests and extended `naming_test.go`/`fn_test.go`).
    - Fix any diffs/findings/failures until all three are clean.
    - _Requirements: 9.1, 9.2, 9.3, 9.5_

  - [x] 7.2 Run composition render over both examples
    - Run `crossplane composition render examples/tenantenvironments/acme-dev.yaml apis/tenantenvironments/composition.yaml`
      and the same for `globex-prod.yaml`; each must exit `0` with no error
      output.
    - Confirm `acme-dev` renders only Bucket + BucketVersioning (no Table) and
      `globex-prod` additionally renders a `Table` carrying
      `crossplane.io/external-name: globex-prod-dtbl`, and that every composed
      resource carries a non-empty external-name annotation.
    - _Requirements: 9.4, 9.6, 9.7, 9.8_

- [x] 8. Final checkpoint - definition of done
  - Ensure all tests pass and both renders succeed, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional (the property tests) and can be skipped for
  a faster MVP, but P-DDB-1..P-DDB-11 are the design's stated correctness guards
  — implement them before considering the slice done.
- Each task references specific requirement clauses (and, for test tasks, the
  design property identifier) for traceability.
- Task 1 does real model generation (unlike the ECR slice, the DynamoDB models
  are not yet generated). The generated models under `schemas/go` are
  tool-generated and must never be hand-edited; if a type is missing the fix is
  to re-add the dependency and regenerate.
- Do not re-run `crossplane xrd generate` or `crossplane composition generate` —
  both are destructive to hand-maintained files.
- Property tests use `pgregory.net/rapid` (rapid's default check count exceeds
  100 iterations) and `github.com/google/go-cmp/cmp` for structural equality,
  consistent with the existing `fn_*_property_test.go` files.
- Composition-resource map keys (`bucket`, `bucket-versioning`, `repository`,
  `table`) are stable identifiers and are not renamed.
- `status.tableName` is derived from naming (like S3 `bucketName`), not read from
  the observed resource's `atProvider` (unlike ECR `repositoryUrl`).

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1", "2.1"] },
    { "id": 1, "tasks": ["2.2", "2.3", "3.1"] },
    { "id": 2, "tasks": ["3.2"] },
    { "id": 3, "tasks": ["3.3"] },
    { "id": 4, "tasks": ["3.4", "3.5", "3.6", "3.7", "3.8", "3.9", "3.10", "3.11", "3.12", "5.1", "6.1"] },
    { "id": 5, "tasks": ["5.2", "6.2"] },
    { "id": 6, "tasks": ["7.1"] },
    { "id": 7, "tasks": ["7.2"] }
  ]
}
```
