# Requirements Document

## Introduction

This feature adds **ECR repository** provisioning to the existing
`TenantEnvironment` control-plane API, following the pattern already established
by the S3 slice. The `TenantEnvironment` API shape and its embedded Go
composition function (`fn.go` / `naming.go`) already exist and compose two
namespaced S3 managed resources (`Bucket` + `BucketVersioning`) unconditionally.
This slice extends the same function so that, when a tenant opts in via
`spec.repository.enabled`, the function additionally composes a namespaced ECR
`Repository`, and populates `status.repositoryUrl` once that repository reports
ready.

The ECR repository is **optional**, gated by `spec.repository.enabled` (default
`false`). When the flag is `false`, the function MUST NOT put the `repository`
key in the desired-resources map at all — exactly as the S3 slice keeps
DynamoDB and ECR out entirely. This mirrors the intended DynamoDB approach: an
`enabled` flag that adds a single managed resource keyed by a stable identifier.

Scope is **ECR only**. The `spec.repository.enabled` field and the
`status.repositoryUrl` status field already exist in the API
(`apis/tenantenvironments/definition.yaml`); this slice implements the actual
composition behind them. S3 behavior is unchanged. DynamoDB remains a separate
future slice. The guiding principle is the simplest thing that works end to end
for ECR: no IAM, no KMS encryption configuration, no connection secrets, no image
scanning configuration beyond provider defaults, no lifecycle policies, no
repository policies, no GitOps, no multi-region, and no tags beyond
`tenant` / `environment` / `managed-by`.

## Glossary

- **Crossplane**: The v2.x control plane that reconciles the composition into AWS managed resources. v2 APIs only — namespaced composite resources (XRs), no claims.
- **TenantEnvironment**: The namespaced composite resource (XR), API group `platform.hello-crossplane.io`, version `v1alpha1`, that a tenant authors to request AWS infrastructure. Lives in namespace `<tenant>-<env>`.
- **Composition_Function**: The embedded Go function in `functions/compose-tenant-environment` (`fn.go` implementing `RunFunction`, `naming.go` for pure naming/validation). Given a `TenantEnvironment`, it produces the desired AWS managed resources.
- **Naming_Module**: `naming.go` — pure string-building and validation with no Crossplane imports, so it is trivially testable.
- **ECR_Provider**: `xpkg.crossplane.io/crossplane-contrib/provider-aws-ecr` (v2.x), the Crossplane provider that reconciles ECR managed resources into AWS.
- **Repository**: The namespaced ECR managed resource — `apiVersion: ecr.aws.m.upbound.io/v1beta1`, `Kind: Repository`. Composition-resource map key `repository`.
- **Bucket**: The namespaced S3 managed resource already composed by this function — `apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: Bucket`, map key `bucket`.
- **BucketVersioning**: The namespaced S3 versioning managed resource already composed by this function — `apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: BucketVersioning`, map key `bucket-versioning`.
- **External_Name**: The `crossplane.io/external-name` annotation on a managed resource that carries the real AWS resource name. Never set via `metadata.name`.
- **Repository_External_Name**: The External_Name of the Repository — the literal `<tenant>-<env>-ecr`.
- **Repository_URL**: The URL of the provisioned ECR repository, in the form `<aws-account-id>.dkr.ecr.<region>.amazonaws.com/<repository-name>`. It is reported by the ECR_Provider in the observed Repository's `status.atProvider.repositoryUrl` and is not derivable by the Composition_Function ahead of provisioning.
- **Generated_Models**: The typed Go models under the `dev.crossplane.io/models` module, produced by `crossplane dependency add`. Never hand-edited.
- **Namespace**: The Kubernetes namespace `<tenant>-<env>` (e.g. `acme-dev`) that holds the XR and all composed managed resources.
- **Enabled**: The boolean `spec.repository.enabled` on the TenantEnvironment, default `false`, that gates whether the Repository is composed.

## Requirements

### Requirement 1: Add the AWS ECR provider dependency

**User Story:** As a platform engineer, I want the AWS ECR provider declared as a project dependency with its Go models generated, so that the composition function can compose a typed ECR managed resource.

#### Acceptance Criteria

1. THE ECR_Provider SHALL be declared as a dependency entry in `crossplane-project.yaml` referencing `xpkg.crossplane.io/crossplane-contrib/provider-aws-ecr` at a version constraint `>=v2.0.0,<v3.0.0` (resolved `v2.7.0` per `schemas/.lock.json`), added via the command `crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-ecr`.
2. WHEN the ECR_Provider dependency is added, THE Generated_Models SHALL be produced under the `dev.crossplane.io/models` module and SHALL include the namespaced (`ecr.aws.m.upbound.io/v1beta1`) `Repository` type.
3. THE Generated_Models SHALL remain byte-for-byte identical to the tool-generated output, containing no hand-applied edits.
4. IF the namespaced ECR `Repository` type is absent from the Generated_Models, THEN THE platform engineer SHALL re-add the ECR_Provider dependency and regenerate the models rather than editing the generated files.
5. WHEN preparing for a build or render, THE platform engineer SHALL run `crossplane dependency update-cache`, after which the ECR_Provider package SHALL resolve from the local cache without a network fetch.
6. IF `crossplane dependency update-cache` fails to populate the ECR_Provider package in the local cache, THEN a subsequent build or render SHALL fail with an error identifying the unresolved ECR_Provider package.

### Requirement 2: Provision the ECR repository when enabled

**User Story:** As a PaaS tenant, I want an ECR repository created for my environment when I opt in, so that I have a private container registry without knowing how it is built.

#### Acceptance Criteria

1. WHERE `spec.repository.enabled` is `true`, THE Composition_Function SHALL include a Repository managed resource in the desired resources under the map key `repository`.
2. WHERE `spec.repository.enabled` is `false`, THE Composition_Function SHALL omit the map key `repository` from the desired resources entirely.
3. WHEN `spec.repository.enabled` is absent, THE Composition_Function SHALL treat the value as `false` (the schema default) and SHALL omit the map key `repository` from the desired resources.
4. THE Composition_Function SHALL use `apiVersion: ecr.aws.m.upbound.io/v1beta1` and `Kind: Repository` (the namespaced `.m.` API group) for the Repository.
5. WHERE the Repository is composed, THE Composition_Function SHALL place the Repository in the Namespace named `<tenant>-<env>`, the same Namespace as the TenantEnvironment and the Bucket, where `<tenant>` is `spec.tenant` and `<env>` is `spec.environment`.
6. WHERE the Repository is composed, THE Composition_Function SHALL set the Repository `spec.forProvider.region` from `spec.region` of the TenantEnvironment.
7. THE Composition_Function SHALL use the literal string `repository` as the composition-resource map key for the Repository.
8. WHERE the Repository is composed, THE Composition_Function SHALL set the `crossplane.io/external-name` annotation on the Repository to the Repository_External_Name, and SHALL NOT set `metadata.name` to the Repository_External_Name or to any value derived from `spec.tenant` and `spec.environment`.

### Requirement 3: Name the ECR repository via the external-name annotation

**User Story:** As a platform engineer, I want the AWS ECR repository name derived deterministically from tenant and environment, so that repository names are predictable and stable across reconciliations.

#### Acceptance Criteria

1. THE Naming_Module SHALL build the Repository_External_Name as the literal concatenation `<tenant>-<env>-ecr`, where `<tenant>` is the value of `spec.tenant` and `<env>` is the value of `spec.environment`.
2. WHEN the Naming_Module builds the Repository_External_Name for the same `spec.tenant` and `spec.environment` values, THE Naming_Module SHALL produce a byte-identical Repository_External_Name on every invocation.
3. WHERE the Repository is composed under the stable composition-resource key `repository` on the namespaced ECR group (`ecr.aws.m.upbound.io/v1beta1`), THE Composition_Function SHALL set the Repository AWS name via the `crossplane.io/external-name` annotation on that Repository, with the annotation value equal to the Repository_External_Name produced by the Naming_Module.
4. THE Composition_Function SHALL NOT set the `metadata.name` field of the Repository to the Repository_External_Name or to any value derived from `spec.tenant` and `spec.environment`.
5. IF `spec.tenant` or `spec.environment` is absent or empty such that the Repository_External_Name cannot be constructed, THEN THE Naming_Module SHALL NOT produce a Repository_External_Name and SHALL return an error identifying the missing input field, and THE Composition_Function SHALL NOT add the Repository to the desired resources and SHALL report the error observably via `response.Fatal` on the TenantEnvironment XR.

### Requirement 4: Tag the ECR repository with the standard tag set

**User Story:** As a platform engineer, I want every composed resource tagged consistently, so that tenant resources are attributable without bespoke tagging logic.

#### Acceptance Criteria

1. WHERE the Repository is composed, THE Composition_Function SHALL set on the Repository `spec.forProvider.tags` exactly the three tag keys `tenant`, `environment`, and `managed-by`, and SHALL NOT set any fourth or additional tag key.
2. WHERE the Repository is composed, THE Composition_Function SHALL set the `tenant` tag value equal to `spec.tenant` and the `environment` tag value equal to `spec.environment` of the TenantEnvironment, with each value matching its source field exactly and carrying no additional prefix, suffix, or whitespace.
3. WHERE the Repository is composed, THE Composition_Function SHALL set the Repository `managed-by` tag value to the fixed literal value `crossplane`, identical to the value applied to the Bucket's `managed-by` tag.
4. WHERE `spec.repository.enabled` is `false`, THE Composition_Function SHALL NOT compose a Repository and therefore SHALL NOT set any `tenant`, `environment`, or `managed-by` tag on a Repository resource.

### Requirement 5: Report the repository URL in status

**User Story:** As a PaaS tenant, I want the provisioned repository URL surfaced in the resource status, so that I can push and pull images without inspecting AWS directly.

#### Acceptance Criteria

1. WHEN the Repository reports ready, THE Composition_Function SHALL set `status.repositoryUrl` of the TenantEnvironment to the Repository_URL read from the observed Repository's `status.atProvider.repositoryUrl`.
2. WHILE the Repository has not reported ready, THE Composition_Function SHALL leave `status.repositoryUrl` unset, where "unset" means the `status.repositoryUrl` field is absent from the TenantEnvironment status.
3. IF the Repository transitions from ready back to not-ready, THEN THE Composition_Function SHALL clear `status.repositoryUrl` so that it reflects only a currently-ready Repository.
4. WHEN the Repository reports ready, THE Composition_Function SHALL set `status.repositoryUrl` to a value that exactly matches the observed `status.atProvider.repositoryUrl` with no additional prefix, suffix, or whitespace.
5. WHERE `spec.repository.enabled` is `false`, THE Composition_Function SHALL leave `status.repositoryUrl` unset regardless of any observed Repository readiness.
6. WHEN the Repository reports ready but the observed Repository does not carry a `status.atProvider.repositoryUrl` value, or that observed value is whitespace-only, THE Composition_Function SHALL treat the value as not carrying a value and SHALL leave `status.repositoryUrl` unset (field absent).
7. THE Composition_Function SHALL derive `status.repositoryUrl` from the current observed Repository state on every reconcile and SHALL NOT latch a previously observed value.

### Requirement 6: Preserve S3 behavior and restrict this slice to S3 and ECR

**User Story:** As a platform engineer, I want this slice limited to adding ECR while leaving S3 intact, so that DynamoDB remains a clean future slice and the PoC stays minimal.

#### Acceptance Criteria

1. THE Composition_Function SHALL continue to compose the Bucket unconditionally under map key `bucket` using `apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: Bucket`, with external-name annotation `<tenant>-<env>-bucket`, unchanged by this slice.
2. THE Composition_Function SHALL continue to compose the BucketVersioning under map key `bucket-versioning` using `apiVersion: s3.aws.m.upbound.io/v1beta1`, `Kind: BucketVersioning`, unchanged by this slice.
3. WHERE `spec.table.enabled` is `true`, `false`, or absent, THE Composition_Function SHALL NOT include a DynamoDB Table resource (Kind `Table`) in the desired resources under any map key in this slice.
4. WHERE `spec.repository.enabled` is `true`, THE Composition_Function SHALL emit exactly three desired resources, keyed `bucket` (Kind `Bucket`), `bucket-versioning` (Kind `BucketVersioning`), and `repository` (Kind `Repository`), and SHALL NOT emit any other resource key.
5. WHERE `spec.repository.enabled` is `false` or absent, THE Composition_Function SHALL emit exactly two desired resources, keyed `bucket` (Kind `Bucket`) and `bucket-versioning` (Kind `BucketVersioning`), and SHALL NOT emit the `repository` key or any other resource key.
6. THE Composition_Function SHALL compose the Repository using only the namespaced group `ecr.aws.m.upbound.io/v1beta1`, and SHALL NOT compose the Repository using the legacy cluster-scoped group `ecr.aws.upbound.io`.
7. THE feature SHALL NOT add any IAM role, IAM policy, KMS encryption configuration, connection secret, image scanning configuration, lifecycle policy, repository policy, GitOps wiring, or multi-region configuration to the desired resources in this slice.
8. THE Composition_Function SHALL NOT set any tag beyond `tenant`, `environment`, and `managed-by` on any resource in this slice.

### Requirement 7: Surface errors through the composition response

**User Story:** As a PaaS tenant, I want ECR provisioning problems reported on my resource, so that I can diagnose failures without reading function pod logs.

#### Acceptance Criteria

1. IF the TenantEnvironment spec cannot be read or is invalid such that the Repository_External_Name cannot be derived, THEN THE Composition_Function SHALL report the problem via `response.Fatal` with a message identifying the specific field name and reason that failed, and SHALL emit zero desired managed resources in the response.
2. WHEN THE Composition_Function reports a problem via `response.Fatal` or `response.Warning`, THE Composition_Function SHALL attach the report to the TenantEnvironment XR received in the request so it appears in the XR's events.
3. THE Composition_Function SHALL wrap internal errors using `github.com/crossplane/crossplane-runtime/v2/pkg/errors` before surfacing them via `response.Fatal` or `response.Warning`, preserving the originating error text.
4. WHERE a non-fatal condition affecting the Repository requires tenant action, THE Composition_Function SHALL report it via `response.Warning` with a message identifying the Repository by its composition-resource key `repository`, and SHALL continue emitting the remaining desired managed resources unchanged.
5. IF the observed TenantEnvironment XR cannot be retrieved from the request, THEN THE Composition_Function SHALL report the problem via `response.Fatal` and SHALL emit zero desired managed resources.

### Requirement 8: Meet the definition of done

**User Story:** As a platform engineer, I want the slice to pass the project's quality gates, so that it is mergeable and renders correctly for every example.

#### Acceptance Criteria

1. WHEN `gofmt -l` is run over the Naming_Module and Composition_Function source files, THE result SHALL be zero lines of output and a success exit status.
2. WHEN `go vet ./...` is run against the Composition_Function package, THE package SHALL exit with a success status and report zero findings.
3. WHEN `go test ./...` is run against the Composition_Function package, THE package SHALL exit with a success status with all tests passing, including at least one table-driven test exercising the Naming_Module's Repository_External_Name derivation.
4. WHEN `crossplane composition render` is run against `examples/tenantenvironments/acme-dev.yaml` (repository disabled) and `examples/tenantenvironments/globex-prod.yaml` (repository enabled), THE render SHALL exit with a success status for each file and emit zero error output.
5. THE Naming_Module SHALL contain zero import statements referencing Crossplane packages.
6. IF `crossplane composition render` fails for any file in `examples/tenantenvironments/`, THEN THE render SHALL exit with a non-success status and emit an error identifying the failing example by file path, and the definition of done SHALL be treated as not met.
7. WHEN `crossplane composition render` succeeds for an example, THE render SHALL emit each composed managed resource carrying a non-empty `crossplane.io/external-name` annotation matching the Naming_Module's derivation for that resource.
8. IF any one of the quality gates is not satisfied, THEN the definition of done SHALL be treated as not met.
## Correctness Properties

These properties are the property-based tests that guard this slice. They follow
the conventions of the existing `fn_*_property_test.go` files: `pgtest/rapid`
generators over valid `TenantEnvironment` inputs, `t.Run`/`rapid.Check`
harnesses, and results compared with `go-cmp`. Each property varies the
`repository.enabled` (and `table.enabled`) flags across the full input space so
the invariant is checked everywhere.

- **P-ECR-1 — Resource count is exactly two when disabled, exactly three when enabled.** FOR ALL valid TenantEnvironment inputs: WHERE `spec.repository.enabled` is `false` or absent, `RunFunction` emits exactly the two desired resources keyed `bucket` and `bucket-versioning`; WHERE `spec.repository.enabled` is `true`, it emits exactly the three keyed `bucket`, `bucket-versioning`, and `repository`, and no other key. (Requirements 2.1, 2.2, 2.3, 6.4, 6.5)
- **P-ECR-2 — Repository group and Kind are correct.** FOR ALL valid inputs with `spec.repository.enabled` `true`: the composed `repository` resource uses `apiVersion: ecr.aws.m.upbound.io/v1beta1` and `Kind: Repository`, never the legacy cluster-scoped group `ecr.aws.upbound.io`. (Requirements 2.4, 6.6)
- **P-ECR-3 — Repository external-name equals `<tenant>-<env>-ecr`.** FOR ALL valid inputs with `spec.repository.enabled` `true`: the composed `repository` carries a `crossplane.io/external-name` annotation exactly equal to `<tenant>-<env>-ecr`, and its `metadata.name` is not that value nor any value derived from tenant/environment. (Requirements 2.8, 3.1, 3.3, 3.4)
- **P-ECR-4 — Repository namespace equals `<tenant>-<env>`.** FOR ALL valid inputs with `spec.repository.enabled` `true`: the composed `repository` lands in the Namespace `<tenant>-<env>`, the same Namespace as the Bucket. (Requirement 2.5)
- **P-ECR-5 — Status repositoryUrl mirrors observed atProvider value only when Ready.** FOR ALL valid inputs paired with a mocked observed Repository: WHEN the Repository reports Ready and carries a non-whitespace `status.atProvider.repositoryUrl`, `status.repositoryUrl` equals that observed value byte-for-byte; otherwise (`enabled` false, not Ready, or missing/whitespace-only observed value) `status.repositoryUrl` is absent. The value is re-derived from the current observed state on every reconcile and never latched. (Requirements 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7)
- **P-ECR-6 — Repository tag set is exactly the three standard tags.** FOR ALL valid inputs with `spec.repository.enabled` `true`: the composed `repository` `spec.forProvider.tags` has exactly the keys `tenant`, `environment`, and `managed-by`, with `tenant`/`environment` equal to the source spec fields and `managed-by` the fixed literal `crossplane`; WHERE disabled, no such tags exist because no Repository is composed. (Requirements 4.1, 4.2, 4.3, 4.4)
- **P-ECR-7 — Invalid spec emits zero resources.** FOR ALL TenantEnvironment XRs whose required spec fields are missing or malformed such that the Repository_External_Name cannot be derived: `RunFunction` reports a fatal result and emits zero desired managed resources. (Requirements 3.5, 7.1, 7.5)
- **P-ECR-8 — Naming is deterministic and rejects empty inputs.** FOR ALL valid `spec.tenant` and `spec.environment` pairs, the Naming_Module produces a byte-identical Repository_External_Name on repeated invocations; IF either input is empty, it produces no name and returns an error identifying the missing field. (Requirements 3.1, 3.2, 3.5)
