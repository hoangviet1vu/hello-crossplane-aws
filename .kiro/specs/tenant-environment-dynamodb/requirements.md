# Requirements Document

## Introduction

This feature adds **DynamoDB table** provisioning to the existing
`TenantEnvironment` control-plane API, following the pattern already established
by the S3 and ECR slices. The `TenantEnvironment` API shape and its embedded Go
composition function (`fn.go` / `naming.go`) already exist and compose two
namespaced S3 managed resources (`Bucket` + `BucketVersioning`) unconditionally
and, when opted in via `spec.repository.enabled`, a namespaced ECR `Repository`.
This slice extends the same function so that, when a tenant opts in via
`spec.table.enabled`, the function additionally composes a namespaced DynamoDB
`Table`, and populates `status.tableName` once that table reports ready.

The DynamoDB table is **optional**, gated by `spec.table.enabled` (default
`false`). When the flag is `false`, the function MUST NOT put the `table` key in
the desired-resources map at all — exactly as the ECR slice keeps the
`repository` key out entirely when disabled. This completes the trio of AWS
resources described in the product steering: an always-created S3 bucket, an
optional ECR repository, and now an optional DynamoDB table.

Scope is **DynamoDB only**. The `spec.table.enabled`, `spec.table.hashKey`, and
`spec.table.billingMode` fields and the `status.tableName` status field already
exist in the API (`apis/tenantenvironments/definition.yaml`); this slice
implements the actual composition behind them. S3 and ECR behavior is unchanged.
The guiding principle is the simplest thing that works end to end for DynamoDB:
no IAM, no KMS encryption configuration, no connection secrets, no global or
local secondary indexes beyond the single partition (hash) key, no provisioned
throughput tuning beyond selecting `billingMode`, no point-in-time recovery or
stream configuration, no GitOps, no multi-region, and no tags beyond
`tenant` / `environment` / `managed-by`.

## Glossary

- **Crossplane**: The v2.x control plane that reconciles the composition into AWS managed resources. v2 APIs only — namespaced composite resources (XRs), no claims.
- **TenantEnvironment**: The namespaced composite resource (XR), API group `platform.hello-crossplane.io`, version `v1alpha1`, that a tenant authors to request AWS infrastructure. Lives in namespace `<tenant>-<env>`.
- **Composition_Function**: The embedded Go function in `functions/compose-tenant-environment` (`fn.go` implementing `RunFunction`, `naming.go` for pure naming/validation). Given a `TenantEnvironment`, it produces the desired AWS managed resources.
- **Naming_Module**: `naming.go` — pure string-building and validation with no Crossplane imports, so it is trivially testable.
- **DynamoDB_Provider**: `xpkg.crossplane.io/crossplane-contrib/provider-aws-dynamodb` (v2.x), the Crossplane provider that reconciles DynamoDB managed resources into AWS.
- **Table**: The namespaced DynamoDB managed resource — `apiVersion: dynamodb.aws.m.upbound.io/v1beta1`, `Kind: Table`. Composition-resource map key `table`.
- **Bucket**: The namespaced S3 managed resource already composed by this function — `apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: Bucket`, map key `bucket`.
- **BucketVersioning**: The namespaced S3 versioning managed resource already composed by this function — `apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: BucketVersioning`, map key `bucket-versioning`.
- **Repository**: The namespaced ECR managed resource optionally composed by this function — `apiVersion: ecr.aws.m.upbound.io/v1beta1`, `Kind: Repository`, map key `repository`.
- **External_Name**: The `crossplane.io/external-name` annotation on a managed resource that carries the real AWS resource name. Never set via `metadata.name`.
- **Table_External_Name**: The External_Name of the Table — the literal `<tenant>-<env>-dtbl`.
- **Hash_Key**: The DynamoDB partition (hash) key name, taken from `spec.table.hashKey` (default `id`).
- **Billing_Mode**: The DynamoDB billing mode, taken from `spec.table.billingMode`, one of `PAY_PER_REQUEST` (default) or `PROVISIONED`.
- **Generated_Models**: The typed Go models under the `dev.crossplane.io/models` module, produced by `crossplane dependency add`. Never hand-edited.
- **Namespace**: The Kubernetes namespace `<tenant>-<env>` (e.g. `acme-dev`) that holds the XR and all composed managed resources.
- **Enabled**: The boolean `spec.table.enabled` on the TenantEnvironment, default `false`, that gates whether the Table is composed.

## Requirements

### Requirement 1: Add the AWS DynamoDB provider dependency

**User Story:** As a platform engineer, I want the AWS DynamoDB provider declared as a project dependency with its Go models generated, so that the composition function can compose a typed DynamoDB managed resource.

#### Acceptance Criteria

1. THE DynamoDB_Provider SHALL be declared as a dependency entry in `crossplane-project.yaml` referencing `xpkg.crossplane.io/crossplane-contrib/provider-aws-dynamodb` at a version constraint in the `v2.x` range, added via the command `crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-dynamodb`.
2. WHEN the DynamoDB_Provider dependency is added, THE Generated_Models SHALL be produced under the `dev.crossplane.io/models` module and SHALL include the namespaced (`dynamodb.aws.m.upbound.io/v1beta1`) `Table` type.
3. THE Generated_Models SHALL remain byte-for-byte identical to the tool-generated output, containing no hand-applied edits.
4. IF the namespaced DynamoDB `Table` type is absent from the Generated_Models, THEN THE platform engineer SHALL re-add the DynamoDB_Provider dependency and regenerate the models rather than editing the generated files.
5. WHEN preparing for a build or render, THE platform engineer SHALL run `crossplane dependency update-cache`, after which the DynamoDB_Provider package SHALL resolve from the local cache without a network fetch.
6. IF `crossplane dependency update-cache` fails to populate the DynamoDB_Provider package in the local cache, THEN a subsequent build or render SHALL fail with an error identifying the unresolved DynamoDB_Provider package.

### Requirement 2: Provision the DynamoDB table when enabled

**User Story:** As a PaaS tenant, I want a DynamoDB table created for my environment when I opt in, so that I have a managed key-value store without knowing how it is built.

#### Acceptance Criteria

1. WHERE `spec.table.enabled` is `true`, THE Composition_Function SHALL include a Table managed resource in the desired resources under the map key `table`.
2. WHERE `spec.table.enabled` is `false`, THE Composition_Function SHALL omit the map key `table` from the desired resources entirely.
3. WHEN `spec.table.enabled` is absent, THE Composition_Function SHALL treat the value as `false` (the schema default) and SHALL omit the map key `table` from the desired resources.
4. THE Composition_Function SHALL use `apiVersion: dynamodb.aws.m.upbound.io/v1beta1` and `Kind: Table` (the namespaced `.m.` API group) for the Table.
5. WHERE the Table is composed, THE Composition_Function SHALL place the Table in the Namespace named `<tenant>-<env>`, the same Namespace as the TenantEnvironment and the Bucket, where `<tenant>` is `spec.tenant` and `<env>` is `spec.environment`.
6. WHERE the Table is composed, THE Composition_Function SHALL set the Table `spec.forProvider.region` from `spec.region` of the TenantEnvironment.
7. THE Composition_Function SHALL use the literal string `table` as the composition-resource map key for the Table.
8. WHERE the Table is composed, THE Composition_Function SHALL set the `crossplane.io/external-name` annotation on the Table to the Table_External_Name, and SHALL NOT set `metadata.name` to the Table_External_Name or to any value derived from `spec.tenant` and `spec.environment`.

### Requirement 3: Name the DynamoDB table via the external-name annotation

**User Story:** As a platform engineer, I want the AWS DynamoDB table name derived deterministically from tenant and environment, so that table names are predictable and stable across reconciliations.

#### Acceptance Criteria

1. THE Naming_Module SHALL build the Table_External_Name as the literal concatenation `<tenant>-<env>-dtbl`, where `<tenant>` is the value of `spec.tenant` and `<env>` is the value of `spec.environment`.
2. WHEN the Naming_Module builds the Table_External_Name for the same `spec.tenant` and `spec.environment` values, THE Naming_Module SHALL produce a byte-identical Table_External_Name on every invocation.
3. WHERE the Table is composed under the stable composition-resource key `table` on the namespaced DynamoDB group (`dynamodb.aws.m.upbound.io/v1beta1`), THE Composition_Function SHALL set the Table AWS name via the `crossplane.io/external-name` annotation on that Table, with the annotation value equal to the Table_External_Name produced by the Naming_Module.
4. THE Composition_Function SHALL NOT set the `metadata.name` field of the Table to the Table_External_Name or to any value derived from `spec.tenant` and `spec.environment`.
5. IF `spec.tenant` or `spec.environment` is absent or empty such that the Table_External_Name cannot be constructed, THEN THE Naming_Module SHALL NOT produce a Table_External_Name and SHALL return an error identifying the missing input field, and THE Composition_Function SHALL NOT add the Table to the desired resources and SHALL report the error observably via `response.Fatal` on the TenantEnvironment XR.

### Requirement 4: Configure the table key schema and billing mode from the spec

**User Story:** As a PaaS tenant, I want to choose my table's partition key and billing mode, so that the table matches my access pattern and cost model.

#### Acceptance Criteria

1. WHERE the Table is composed, THE Composition_Function SHALL set the Table `spec.forProvider.hashKey` to the value of `spec.table.hashKey` of the TenantEnvironment.
2. WHEN `spec.table.hashKey` is absent, THE Composition_Function SHALL treat the value as `id` (the schema default) and set the Table `spec.forProvider.hashKey` to `id`.
3. WHERE the Table is composed, THE Composition_Function SHALL declare in the Table `spec.forProvider.attribute` exactly one attribute whose `name` equals the Hash_Key and whose `type` is `S`.
4. WHERE the Table is composed, THE Composition_Function SHALL set the Table `spec.forProvider.billingMode` to the value of `spec.table.billingMode` of the TenantEnvironment.
5. WHEN `spec.table.billingMode` is absent, THE Composition_Function SHALL treat the value as `PAY_PER_REQUEST` (the schema default) and set the Table `spec.forProvider.billingMode` to `PAY_PER_REQUEST`.
6. WHERE the Table is composed AND the Billing_Mode is `PAY_PER_REQUEST`, THE Composition_Function SHALL NOT set the Table `spec.forProvider.readCapacity` or `spec.forProvider.writeCapacity`.
7. WHERE the Table is composed AND the Billing_Mode is `PROVISIONED`, THE Composition_Function SHALL set the Table `spec.forProvider.readCapacity` and `spec.forProvider.writeCapacity` each to the value `1`.

### Requirement 5: Tag the DynamoDB table with the standard tag set

**User Story:** As a platform engineer, I want every composed resource tagged consistently, so that tenant resources are attributable without bespoke tagging logic.

#### Acceptance Criteria

1. WHERE the Table is composed, THE Composition_Function SHALL set on the Table `spec.forProvider.tags` exactly the three tag keys `tenant`, `environment`, and `managed-by`, and SHALL NOT set any fourth or additional tag key.
2. WHERE the Table is composed, THE Composition_Function SHALL set the `tenant` tag value equal to `spec.tenant` and the `environment` tag value equal to `spec.environment` of the TenantEnvironment, with each value matching its source field exactly and carrying no additional prefix, suffix, or whitespace.
3. WHERE the Table is composed, THE Composition_Function SHALL set the Table `managed-by` tag value to the fixed literal value `crossplane`, identical to the value applied to the Bucket's `managed-by` tag.
4. WHERE `spec.table.enabled` is `false`, THE Composition_Function SHALL NOT compose a Table and therefore SHALL NOT set any `tenant`, `environment`, or `managed-by` tag on a Table resource.

### Requirement 6: Report the table name in status

**User Story:** As a PaaS tenant, I want the provisioned table name surfaced in the resource status, so that I can reference it without inspecting AWS directly.

#### Acceptance Criteria

1. WHEN the Table reports ready, THE Composition_Function SHALL set `status.tableName` of the TenantEnvironment to the Table_External_Name value.
2. WHILE the Table has not reported ready, THE Composition_Function SHALL leave `status.tableName` unset, where "unset" means the `status.tableName` field is absent from the TenantEnvironment status.
3. IF the Table transitions from ready back to not-ready, THEN THE Composition_Function SHALL clear `status.tableName` so that it reflects only a currently-ready Table.
4. WHEN the Table reports ready, THE Composition_Function SHALL set `status.tableName` to a value that exactly matches the Table_External_Name with no additional prefix, suffix, or whitespace.
5. WHERE `spec.table.enabled` is `false`, THE Composition_Function SHALL leave `status.tableName` unset regardless of any observed Table readiness.
6. THE Composition_Function SHALL derive `status.tableName` from the current observed Table state on every reconcile and SHALL NOT latch a previously observed value.

### Requirement 7: Preserve S3 and ECR behavior and restrict this slice to DynamoDB

**User Story:** As a platform engineer, I want this slice limited to adding DynamoDB while leaving S3 and ECR intact, so that the PoC completes its resource trio without regressions and stays minimal.

#### Acceptance Criteria

1. THE Composition_Function SHALL continue to compose the Bucket unconditionally under map key `bucket` using `apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: Bucket`, with external-name annotation `<tenant>-<env>-bucket`, unchanged by this slice.
2. THE Composition_Function SHALL continue to compose the BucketVersioning under map key `bucket-versioning` using `apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: BucketVersioning`, unchanged by this slice.
3. WHERE `spec.repository.enabled` is `true`, THE Composition_Function SHALL continue to compose the Repository under map key `repository` using `apiVersion: ecr.aws.m.upbound.io/v1beta1`, `Kind: Repository`, unchanged by this slice.
4. WHERE `spec.table.enabled` is `true` AND `spec.repository.enabled` is `false` or absent, THE Composition_Function SHALL emit exactly three desired resources, keyed `bucket` (Kind `Bucket`), `bucket-versioning` (Kind `BucketVersioning`), and `table` (Kind `Table`), and SHALL NOT emit any other resource key.
5. WHERE `spec.table.enabled` is `false` or absent, THE Composition_Function SHALL NOT emit the `table` key in the desired resources under any circumstance.
6. WHERE `spec.table.enabled` is `true` AND `spec.repository.enabled` is `true`, THE Composition_Function SHALL emit exactly four desired resources, keyed `bucket` (Kind `Bucket`), `bucket-versioning` (Kind `BucketVersioning`), `repository` (Kind `Repository`), and `table` (Kind `Table`), and SHALL NOT emit any other resource key.
7. THE Composition_Function SHALL compose the Table using only the namespaced group `dynamodb.aws.m.upbound.io/v1beta1`, and SHALL NOT compose the Table using the legacy cluster-scoped group `dynamodb.aws.upbound.io`.
8. THE feature SHALL NOT add any IAM role, IAM policy, KMS encryption configuration, connection secret, secondary index beyond the single Hash_Key, point-in-time recovery configuration, stream configuration, GitOps wiring, or multi-region configuration to the desired resources in this slice.
9. THE Composition_Function SHALL NOT set any tag beyond `tenant`, `environment`, and `managed-by` on any resource in this slice.

### Requirement 8: Surface errors through the composition response

**User Story:** As a PaaS tenant, I want DynamoDB provisioning problems reported on my resource, so that I can diagnose failures without reading function pod logs.

#### Acceptance Criteria

1. IF the TenantEnvironment spec cannot be read or is invalid such that the Table_External_Name cannot be derived, THEN THE Composition_Function SHALL report the problem via `response.Fatal` with a message identifying the specific field name and reason that failed, and SHALL emit zero desired managed resources in the response.
2. WHEN THE Composition_Function reports a problem via `response.Fatal` or `response.Warning`, THE Composition_Function SHALL attach the report to the TenantEnvironment XR received in the request so it appears in the XR's events.
3. THE Composition_Function SHALL wrap internal errors using `github.com/crossplane/crossplane-runtime/v2/pkg/errors` before surfacing them via `response.Fatal` or `response.Warning`, preserving the originating error text.
4. WHERE a non-fatal condition affecting the Table requires tenant action, THE Composition_Function SHALL report it via `response.Warning` with a message identifying the Table by its composition-resource key `table`, and SHALL continue emitting the remaining desired managed resources unchanged.
5. IF the observed TenantEnvironment XR cannot be retrieved from the request, THEN THE Composition_Function SHALL report the problem via `response.Fatal` and SHALL emit zero desired managed resources.

### Requirement 9: Meet the definition of done

**User Story:** As a platform engineer, I want the slice to pass the project's quality gates, so that it is mergeable and renders correctly for every example.

#### Acceptance Criteria

1. WHEN `gofmt -l` is run over the Naming_Module and Composition_Function source files, THE result SHALL be zero lines of output and a success exit status.
2. WHEN `go vet ./...` is run against the Composition_Function package, THE package SHALL exit with a success status and report zero findings.
3. WHEN `go test ./...` is run against the Composition_Function package, THE package SHALL exit with a success status with all tests passing, including at least one table-driven test exercising the Naming_Module's Table_External_Name derivation.
4. WHEN `crossplane composition render` is run against `examples/tenantenvironments/acme-dev.yaml` (table disabled) and `examples/tenantenvironments/globex-prod.yaml` (table enabled), THE render SHALL exit with a success status for each file and emit zero error output.
5. THE Naming_Module SHALL contain zero import statements referencing Crossplane packages.
6. IF `crossplane composition render` fails for any file in `examples/tenantenvironments/`, THEN THE render SHALL exit with a non-success status and emit an error identifying the failing example by file path, and the definition of done SHALL be treated as not met.
7. WHEN `crossplane composition render` succeeds for an example, THE render SHALL emit each composed managed resource carrying a non-empty `crossplane.io/external-name` annotation matching the Naming_Module's derivation for that resource.
8. IF any one of the quality gates is not satisfied, THEN the definition of done SHALL be treated as not met.

## Correctness Properties

These properties are the property-based tests that guard this slice. They follow
the conventions of the existing `fn_*_property_test.go` files: `pgtest/rapid`
generators over valid `TenantEnvironment` inputs, `t.Run`/`rapid.Check`
harnesses, and results compared with `go-cmp`. Each property varies the
`table.enabled` (and `repository.enabled`) flags across the full input space so
the invariant is checked everywhere.

- **P-DDB-1 — Resource count reflects the enabled flags exactly.** FOR ALL valid TenantEnvironment inputs: WHERE `spec.table.enabled` is `false` or absent, `RunFunction` does not emit the `table` key; WHERE `spec.table.enabled` is `true`, it emits exactly one `table` resource in addition to the always-present `bucket` and `bucket-versioning` (and the `repository` iff `spec.repository.enabled` is `true`), and no other key. (Requirements 2.1, 2.2, 2.3, 7.4, 7.5, 7.6)
- **P-DDB-2 — Table group and Kind are correct.** FOR ALL valid inputs with `spec.table.enabled` `true`: the composed `table` resource uses `apiVersion: dynamodb.aws.m.upbound.io/v1beta1` and `Kind: Table`, never the legacy cluster-scoped group `dynamodb.aws.upbound.io`. (Requirements 2.4, 7.7)
- **P-DDB-3 — Table external-name equals `<tenant>-<env>-dtbl`.** FOR ALL valid inputs with `spec.table.enabled` `true`: the composed `table` carries a `crossplane.io/external-name` annotation exactly equal to `<tenant>-<env>-dtbl`, and its `metadata.name` is not that value nor any value derived from tenant/environment. (Requirements 2.8, 3.1, 3.3, 3.4)
- **P-DDB-4 — Table namespace equals `<tenant>-<env>`.** FOR ALL valid inputs with `spec.table.enabled` `true`: the composed `table` lands in the Namespace `<tenant>-<env>`, the same Namespace as the Bucket. (Requirement 2.5)
- **P-DDB-5 — Key schema mirrors the spec.** FOR ALL valid inputs with `spec.table.enabled` `true`: the composed `table` has `spec.forProvider.hashKey` equal to `spec.table.hashKey` (defaulting to `id` when absent), and declares exactly one attribute whose `name` equals that hash key and whose `type` is `S`. (Requirements 4.1, 4.2, 4.3)
- **P-DDB-6 — Billing mode and capacity are consistent.** FOR ALL valid inputs with `spec.table.enabled` `true`: the composed `table` has `spec.forProvider.billingMode` equal to `spec.table.billingMode` (defaulting to `PAY_PER_REQUEST` when absent); WHERE the mode is `PAY_PER_REQUEST` no `readCapacity`/`writeCapacity` is set, and WHERE the mode is `PROVISIONED` both are set to `1`. (Requirements 4.4, 4.5, 4.6, 4.7)
- **P-DDB-7 — Status tableName mirrors external name only when Ready.** FOR ALL valid inputs paired with a mocked observed Table: WHEN the Table reports Ready, `status.tableName` equals the Table_External_Name byte-for-byte; otherwise (`enabled` false, or not Ready) `status.tableName` is absent. The value is re-derived from the current observed state on every reconcile and never latched. (Requirements 6.1, 6.2, 6.3, 6.4, 6.5, 6.6)
- **P-DDB-8 — Table tag set is exactly the three standard tags.** FOR ALL valid inputs with `spec.table.enabled` `true`: the composed `table` `spec.forProvider.tags` has exactly the keys `tenant`, `environment`, and `managed-by`, with `tenant`/`environment` equal to the source spec fields and `managed-by` the fixed literal `crossplane`; WHERE disabled, no such tags exist because no Table is composed. (Requirements 5.1, 5.2, 5.3, 5.4)
- **P-DDB-9 — Invalid spec emits zero resources.** FOR ALL TenantEnvironment XRs whose required spec fields are missing or malformed such that the Table_External_Name cannot be derived: `RunFunction` reports a fatal result and emits zero desired managed resources. (Requirements 3.5, 8.1, 8.5)
- **P-DDB-10 — Naming is deterministic and rejects empty inputs.** FOR ALL valid `spec.tenant` and `spec.environment` pairs, the Naming_Module produces a byte-identical Table_External_Name on repeated invocations; IF either input is empty, it produces no name and returns an error identifying the missing field. (Requirements 3.1, 3.2, 3.5)
- **P-DDB-11 — S3 and ECR behavior is unchanged.** FOR ALL valid inputs: the composed `bucket`, `bucket-versioning`, and (where `spec.repository.enabled` is `true`) `repository` resources are byte-for-byte identical to the output produced before this slice, regardless of the `spec.table.*` values. (Requirements 7.1, 7.2, 7.3)
