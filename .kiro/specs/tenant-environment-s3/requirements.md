# Requirements Document

## Introduction

This feature delivers the first vertical slice of the `hello-crossplane-aws`
control plane project: end-to-end provisioning of an **S3 bucket** for a
`TenantEnvironment`. The `TenantEnvironment` API shape already exists in
`apis/tenantenvironments/definition.yaml`; this slice adds the AWS S3 provider as
a project dependency and implements the embedded Go composition function so that
applying a `TenantEnvironment` produces a namespaced S3 `Bucket` (always) and a
`BucketVersioning` managed resource, and populates `status.bucketName` once the
bucket reports ready.

Scope is **S3 only**. DynamoDB tables and ECR repositories are explicitly
deferred to later slices, even though their fields already exist in the API. The
guiding principle is the simplest thing that works end to end for S3. No IAM,
KMS, connection secrets, GitOps, multi-region, or extra tags.

## Glossary

- **Crossplane**: The v2.x control plane that reconciles the composition into AWS managed resources. v2 APIs only — namespaced composite resources (XRs), no claims.
- **TenantEnvironment**: The namespaced composite resource (XR), API group `platform.hello-crossplane.io`, version `v1alpha1`, that a tenant authors to request AWS infrastructure. Lives in namespace `<tenant>-<env>`.
- **Composition_Function**: The embedded Go function in `functions/compose-tenant-environment` (`fn.go` implementing `RunFunction`, `naming.go` for pure naming/validation). Given a `TenantEnvironment`, it produces the desired S3 managed resources.
- **Naming_Module**: `naming.go` — pure string-building and validation with no Crossplane imports, so it is trivially testable.
- **S3_Provider**: `xpkg.crossplane.io/crossplane-contrib/provider-aws-s3` (v2.x), the Crossplane provider that reconciles S3 managed resources into AWS.
- **Bucket**: The namespaced S3 managed resource — `apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: Bucket`. Composition-resource map key `bucket`.
- **BucketVersioning**: The namespaced S3 versioning managed resource — `apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: BucketVersioning`. Composition-resource map key `bucket-versioning`.
- **External_Name**: The `crossplane.io/external-name` annotation on a managed resource that carries the real AWS resource name. Never set via `metadata.name`.
- **Generated_Models**: The typed Go models under the `dev.crossplane.io/models` module, produced by `crossplane dependency add`. Never hand-edited.
- **Namespace**: The Kubernetes namespace `<tenant>-<env>` (e.g. `acme-dev`) that holds the XR and all composed managed resources.

## Requirements

### Requirement 1: Add the AWS S3 provider dependency

**User Story:** As a platform engineer, I want the AWS S3 provider declared as a project dependency with its Go models generated, so that the composition function can compose typed S3 managed resources.

#### Acceptance Criteria

1. THE S3_Provider SHALL be declared as a dependency entry in `crossplane-project.yaml` referencing `xpkg.crossplane.io/crossplane-contrib/provider-aws-s3` at a version in the `v2.x` range, added via the command `crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-s3`.
2. WHEN the S3_Provider dependency is added, THE Generated_Models SHALL be produced under the `dev.crossplane.io/models` module and SHALL include the namespaced (`s3.aws.m.upbound.io/v1beta1`) `Bucket` and `BucketVersioning` types.
3. THE Generated_Models SHALL remain byte-for-byte identical to the tool-generated output, containing no hand-applied edits.
4. IF a required S3 managed resource type is absent from the Generated_Models, THEN THE platform engineer SHALL re-add the S3_Provider dependency and regenerate the models rather than editing the generated files.
5. WHEN preparing for a build or render, THE platform engineer SHALL run `crossplane dependency update-cache`, after which the S3_Provider package SHALL resolve from the local cache without a network fetch.

### Requirement 2: Provision the S3 bucket for every TenantEnvironment

**User Story:** As a PaaS tenant, I want an S3 bucket created for my environment, so that I have durable object storage without knowing how it is built.

#### Acceptance Criteria

1. WHEN a TenantEnvironment is reconciled, THE Composition_Function SHALL include a Bucket managed resource in the desired resources under the map key `bucket`.
2. THE Composition_Function SHALL create the Bucket unconditionally for every TenantEnvironment, independent of the values of `spec.table.enabled` and `spec.repository.enabled`.
3. THE Bucket SHALL use `apiVersion: s3.aws.m.upbound.io/v1beta1` and `Kind: Bucket` (the namespaced `.m.` API group).
4. THE Composition_Function SHALL place the Bucket in the Namespace named `<tenant>-<env>`, the same namespace as the TenantEnvironment, where `<tenant>` is `spec.tenant` and `<env>` is `spec.environment`.
5. THE Composition_Function SHALL set the Bucket `spec.forProvider.region` from `spec.region` of the TenantEnvironment.
6. THE Composition_Function SHALL set the Bucket `crossplane.io/external-name` annotation to `<tenant>-<env>-bucket`, deriving the value from `spec.tenant` and `spec.environment`, and SHALL NOT set the AWS bucket name via `metadata.name`.
7. IF `spec.tenant` or `spec.environment` is absent or does not match the schema pattern such that the `<tenant>-<env>-bucket` external name cannot be constructed, THEN THE Composition_Function SHALL NOT include the Bucket in the desired resources and SHALL surface an error indicating the external name could not be derived, leaving prior desired resources unchanged.

### Requirement 3: Name the S3 bucket via the external-name annotation

**User Story:** As a platform engineer, I want the AWS bucket name derived deterministically from tenant and environment, so that bucket names are predictable and stable across reconciliations.

#### Acceptance Criteria

1. THE Naming_Module SHALL build the bucket External_Name as the literal concatenation `<tenant>-<env>-bucket`, where `<tenant>` is the value of `spec.tenant` and `<env>` is the value of `spec.environment`.
2. WHEN the Naming_Module builds the bucket External_Name for the same `spec.tenant` and `spec.environment` values, THE Naming_Module SHALL produce a byte-identical External_Name on every invocation.
3. THE Composition_Function SHALL set the bucket AWS name via the `crossplane.io/external-name` annotation on the Bucket, with the annotation value equal to the External_Name produced by the Naming_Module.
4. THE Composition_Function SHALL NOT set the `metadata.name` field of the Bucket to the bucket External_Name or to any value derived from `spec.tenant` and `spec.environment`.
5. THE Composition_Function SHALL use the literal string `bucket` as the composition-resource map key for the Bucket.
6. IF `spec.tenant` or `spec.environment` is absent or empty, THEN THE Naming_Module SHALL NOT produce a bucket External_Name and SHALL return an error indicating the missing input, and THE Composition_Function SHALL NOT add the Bucket to the composition-resource map.

### Requirement 4: Gate S3 bucket versioning on the spec

**User Story:** As a PaaS tenant, I want to control whether my bucket keeps object versions, so that I can enable version history when I need it.

#### Acceptance Criteria

1. THE Composition_Function SHALL include exactly one BucketVersioning managed resource in the desired resources under the map key `bucket-versioning`.
2. THE Composition_Function SHALL set the BucketVersioning to `apiVersion: s3.aws.m.upbound.io/v1beta1` and `Kind: BucketVersioning` (the namespaced `.m.` API group).
3. WHERE `spec.bucket.versioning` is `true`, THE Composition_Function SHALL set the BucketVersioning versioning configuration status to `Enabled` and to no other value.
4. WHERE `spec.bucket.versioning` is `false`, THE Composition_Function SHALL set the BucketVersioning versioning configuration status to `Suspended` and to no other value.
5. WHEN `spec.bucket.versioning` is absent, THE Composition_Function SHALL treat the value as `true` (the schema default) and set the BucketVersioning versioning configuration status to `Enabled`.
6. THE Composition_Function SHALL place the BucketVersioning in the Namespace `<tenant>-<env>`, matching the Namespace of the Bucket under map key `bucket`.
7. THE Composition_Function SHALL configure the BucketVersioning to reference the Bucket emitted under map key `bucket`.
8. IF the Bucket under map key `bucket` is absent from the desired resources, THEN THE Composition_Function SHALL not emit the BucketVersioning resource and SHALL surface an error indicating the referenced Bucket is missing.

### Requirement 5: Report the bucket name in status

**User Story:** As a PaaS tenant, I want the provisioned bucket name surfaced in the resource status, so that I can reference it without inspecting AWS directly.

#### Acceptance Criteria

1. WHEN the Bucket reports ready, THE Composition_Function SHALL set `status.bucketName` of the TenantEnvironment to the Bucket External_Name value.
2. WHILE the Bucket has not reported ready, THE Composition_Function SHALL leave `status.bucketName` unset.
3. IF the Bucket transitions from ready back to not-ready, THEN THE Composition_Function SHALL clear `status.bucketName` so that it reflects only a currently-ready Bucket.
4. WHEN the Bucket reports ready, THE Composition_Function SHALL set `status.bucketName` to a value that exactly matches the Bucket External_Name with no additional prefix, suffix, or whitespace.
5. THE Composition_Function SHALL leave `status.tableName` unset in this slice.
6. THE Composition_Function SHALL leave `status.repositoryUrl` unset in this slice.

### Requirement 6: Restrict this slice to S3 resources

**User Story:** As a platform engineer, I want this slice limited to S3, so that DynamoDB and ECR remain clean future slices and the PoC stays minimal.

#### Acceptance Criteria

1. THE Composition_Function SHALL NOT include a DynamoDB Table resource (Kind `Table`) in the desired resources map in this slice, regardless of the value of `spec.table.enabled`.
2. THE Composition_Function SHALL NOT include an ECR Repository resource (Kind `Repository`) in the desired resources map in this slice, regardless of the value of `spec.repository.enabled`.
3. THE Composition_Function SHALL emit exactly two desired resources in this slice, keyed `bucket` (Kind `Bucket`) and `bucket-versioning` (Kind `BucketVersioning`), and SHALL NOT emit any other resource key.
4. THE Composition_Function SHALL compose every S3 resource using only the namespaced group `s3.aws.m.upbound.io/v1beta1`, and SHALL NOT compose any resource using the legacy cluster-scoped group `s3.aws.upbound.io`.
5. WHEN a composed S3 resource is emitted, THE Composition_Function SHALL set only the tag keys `tenant`, `environment`, and `managed-by`, and SHALL NOT set any additional tag key.
6. THE feature SHALL NOT add any IAM role, IAM policy, KMS encryption configuration, connection secret, GitOps wiring, or multi-region configuration to the desired resources in this slice.

### Requirement 7: Surface errors through the composition response

**User Story:** As a PaaS tenant, I want provisioning problems reported on my resource, so that I can diagnose failures without reading function pod logs.

#### Acceptance Criteria

1. IF the TenantEnvironment spec cannot be read or is invalid, THEN THE Composition_Function SHALL report the problem via `response.Fatal` with a message identifying the field or reason that failed, and SHALL NOT emit any desired managed resources in the response.
2. WHEN THE Composition_Function reports a problem via `response.Fatal` or `response.Warning`, THE Composition_Function SHALL attach the report to the TenantEnvironment XR so it appears in the XR's events.
3. THE Composition_Function SHALL wrap internal errors using `github.com/crossplane/crossplane-runtime/v2/pkg/errors` before surfacing them via `response.Fatal` or `response.Warning`.
4. WHERE a non-fatal condition needs the tenant's attention, THE Composition_Function SHALL report it via `response.Warning` with a message identifying the affected resource or condition, and SHALL continue emitting the remaining desired managed resources.

### Requirement 8: Meet the definition of done

**User Story:** As a platform engineer, I want the slice to pass the project's quality gates, so that it is mergeable and renders correctly for every example.

#### Acceptance Criteria

1. WHEN `gofmt -l` is run over the Naming_Module and Composition_Function source files, THE Naming_Module and Composition_Function SHALL produce zero lines of output (an empty result indicating no formatting differences).
2. WHEN `go vet ./...` is run against the Composition_Function package, THE Composition_Function package SHALL exit with a success status and report zero findings.
3. WHEN `go test ./...` is run against the Composition_Function package, THE Composition_Function package SHALL exit with a success status with all tests passing, including at least one table-driven test exercising the Naming_Module.
4. WHEN `crossplane composition render` is run against `examples/tenantenvironments/acme-dev.yaml` (table and repository disabled) and `examples/tenantenvironments/globex-prod.yaml` (versioning enabled), THE render SHALL exit with a success status for each file and emit no error.
5. THE Naming_Module SHALL contain zero import statements referencing Crossplane packages, so its naming logic is unit-testable in isolation.
6. IF `crossplane composition render` fails for any file in `examples/tenantenvironments/`, THEN THE render SHALL exit with a non-success status and emit an error message identifying the failing example, and the definition of done SHALL be treated as not met.
