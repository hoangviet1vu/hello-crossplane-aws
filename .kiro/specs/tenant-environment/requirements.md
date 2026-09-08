# Requirements Document

## Introduction

This feature defines the `TenantEnvironment` API and its schema for the
`hello-crossplane-aws` control plane project. It is the first implementation
slice: the self-service Kubernetes API surface that a PaaS tenant authors as
YAML, plus the validation and status contract that surrounds it.

The deliverables of this slice are the API definition artifacts only:

- `apis/tenantenvironments/schema.yaml` — the SimpleSchema source, kept as
  documentation of the original shape.
- `apis/tenantenvironments/definition.yaml` — the XRD
  (`apiextensions.crossplane.io/v2`, `spec.scope: Namespaced`), generated once
  from the schema and then hand-maintained to carry the CEL validation rules
  that SimpleSchema cannot express.
- `examples/tenantenvironments/` — sample `TenantEnvironment` custom resources
  that exercise the schema, including the table- and repository-disabled
  variants.

This is a proof of concept. The scope is the *what a tenant may declare and how
the API server validates it* contract. The composition function that turns a
`TenantEnvironment` into AWS managed resources is follow-on work and is out of
scope here, except where the schema must reserve status fields for it.

## Glossary

- **TenantEnvironment**: The composite resource (XR) Kind published by this
  project. One instance represents one tenant's kit in one environment. Group
  `platform.hello-crossplane.io`, plural `tenantenvironments`, version
  `v1alpha1`.
- **XRD**: CompositeResourceDefinition. The `apiextensions.crossplane.io/v2`
  resource that registers the `TenantEnvironment` Kind as a Kubernetes API
  endpoint. File: `definition.yaml`.
- **SimpleSchema**: The Crossplane CLI schema dialect authored in `schema.yaml`
  and used to generate the XRD once.
- **CEL**: Common Expression Language. Kubernetes `x-kubernetes-validations`
  rules embedded in the XRD; used here for immutability transition checks.
- **API_Server**: The Kubernetes API server that validates a `TenantEnvironment`
  against the XRD's OpenAPI schema and CEL rules on create and update.
- **Tenant**: The value of `spec.tenant`. Identifies the tenant owning the
  environment.
- **Environment**: The value of `spec.environment`. One of `dev`, `staging`,
  `prod`.
- **Namespace**: The Kubernetes namespace `<tenant>-<env>` that a
  `TenantEnvironment` lives in and is named after.
- **supported-region allow-list**: The finite set of AWS regions permitted by
  the `spec.region` enum, with `ap-southeast-1` as the default. The exact list
  of members is still open and will be pinned during design.

## Requirements

### Requirement 1: API identity and registration

**User Story:** As a platform engineer, I want the `TenantEnvironment` Kind
registered as a namespaced Crossplane v2 API, so that tenants can create it in
their own namespace through standard Kubernetes tooling.

#### Acceptance Criteria

1. THE XRD SHALL declare `apiVersion: apiextensions.crossplane.io/v2`.
2. THE XRD SHALL define the group as `platform.hello-crossplane.io`.
3. THE XRD SHALL define the Kind as `TenantEnvironment` with plural
   `tenantenvironments`.
4. THE XRD SHALL define exactly one version, named `v1alpha1`, with both
   `served` set to `true` and `referenceable` set to `true`.
5. THE XRD SHALL set `spec.scope` to `Namespaced`.
6. THE XRD SHALL omit any claim configuration, containing no `claimNames` field.
7. WHEN the XRD is applied to the cluster, THE XRD SHALL reach an `Established`
   condition of `True` within 60 seconds.
8. WHEN the XRD reaches an `Established` condition of `True`, THE API_Server
   SHALL serve the `tenantenvironments` resource under group
   `platform.hello-crossplane.io`, version `v1alpha1`, scoped to a namespace.
9. IF a `TenantEnvironment` resource is created without a target namespace, THEN
   THE API_Server SHALL reject the request and retain no resource.

### Requirement 2: Tenant identity fields

**User Story:** As a tenant, I want to declare which tenant and environment an
instance belongs to, so that the platform can scope and name the resulting AWS
kit unambiguously.

#### Acceptance Criteria

1. THE TenantEnvironment SHALL require `spec.tenant` as a string.
2. IF `spec.tenant` does not match the pattern
   `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$` (3 to 22 characters), THEN THE
   API_Server SHALL reject the request, retain no partial resource, and return a
   validation error indicating the tenant pattern violation.
3. THE TenantEnvironment SHALL require `spec.environment` as an enum restricted
   to exactly the values `dev`, `staging`, and `prod`.
4. IF a create or update request omits `spec.tenant` or `spec.environment`, or
   supplies an `environment` value outside the enum, THEN THE API_Server SHALL
   reject the request, retain no partial resource, and return a validation
   error.

### Requirement 3: Immutability of identity fields

**User Story:** As a platform engineer, I want tenant and environment to be
immutable after creation, so that a live instance can never be repointed at a
different tenant's or environment's AWS resources.

#### Acceptance Criteria

1. THE definition.yaml SHALL carry a CEL rule `self == oldSelf` on `spec.tenant`
   with the message `tenant is immutable`.
2. THE definition.yaml SHALL carry a CEL rule `self == oldSelf` on
   `spec.environment` with the message `environment is immutable`.
3. WHEN an update request changes the value of `spec.tenant` to any value
   differing from its value at creation, THE API_Server SHALL reject the
   request, leave the persisted `spec.tenant` value unchanged, and return a
   validation error indicating that tenant is immutable.
4. WHEN an update request changes the value of `spec.environment` to any value
   differing from its value at creation, THE API_Server SHALL reject the
   request, leave the persisted `spec.environment` value unchanged, and return a
   validation error indicating that environment is immutable.
5. WHEN an update request leaves both `spec.tenant` and `spec.environment` equal
   to their values at creation, THE API_Server SHALL accept the request.
6. WHEN a create request is submitted for a new TenantEnvironment, THE
   API_Server SHALL accept any schema-valid `spec.tenant` and `spec.environment`
   values without applying the immutability rules.

### Requirement 4: Region selection

**User Story:** As a tenant, I want to select an AWS region from a supported
list, so that my resources are created in an approved location without needing
to know how they are built.

#### Acceptance Criteria

1. THE TenantEnvironment SHALL accept `spec.region` as a string enum whose
   permitted values are exactly the members of the supported-region allow-list.
2. WHERE `spec.region` is omitted from the submitted resource, THE API_Server
   SHALL set `spec.region` to `ap-southeast-1`.
3. IF `spec.region` is a value that is not a member of the supported-region
   allow-list, THEN THE API_Server SHALL reject the request, retain no partial
   resource, and return a validation error indicating that the region is not in
   the supported-region allow-list.
4. IF `spec.region` is present but is not a string, THEN THE API_Server SHALL
   reject the request, retain no partial resource, and return a validation error
   indicating that the region value is invalid.

### Requirement 5: S3 bucket configuration

**User Story:** As a tenant, I want an S3 bucket provisioned by default with
versioning I can control, so that I always get durable storage without extra
declaration.

#### Acceptance Criteria

1. THE TenantEnvironment SHALL accept `spec.bucket.versioning` as a boolean.
2. WHERE `spec.bucket.versioning` is omitted, THE API_Server SHALL default
   `spec.bucket.versioning` to `true`.
3. IF `spec.bucket.versioning` is present but is not a boolean, THEN THE
   API_Server SHALL reject the request and return a validation error.

### Requirement 6: Optional DynamoDB table configuration

**User Story:** As a tenant, I want to optionally request a DynamoDB table with
a configurable key and billing mode, so that I can add a table only when I need
one.

#### Acceptance Criteria

1. THE TenantEnvironment SHALL accept `spec.table.enabled` as a boolean.
2. WHERE `spec.table.enabled` is omitted, THE API_Server SHALL default
   `spec.table.enabled` to `false`.
3. THE TenantEnvironment SHALL accept `spec.table.hashKey` as a string.
4. WHERE `spec.table.hashKey` is omitted, THE API_Server SHALL default
   `spec.table.hashKey` to `id`.
5. THE TenantEnvironment SHALL accept `spec.table.billingMode` as an enum
   restricted to `PAY_PER_REQUEST` and `PROVISIONED`.
6. WHERE `spec.table.billingMode` is omitted, THE API_Server SHALL default
   `spec.table.billingMode` to `PAY_PER_REQUEST`.
7. IF `spec.table.billingMode` is a value outside the enum, THEN THE API_Server
   SHALL reject the request and return a validation error.

### Requirement 7: Optional ECR repository configuration

**User Story:** As a tenant, I want to optionally request an ECR repository, so
that I can store container images only when I need one.

#### Acceptance Criteria

1. THE TenantEnvironment SHALL accept `spec.repository.enabled` as a boolean.
2. WHERE `spec.repository.enabled` is omitted, THE API_Server SHALL default
   `spec.repository.enabled` to `false`.
3. IF `spec.repository.enabled` is set to a non-boolean value, THEN THE
   API_Server SHALL reject the request and return a validation error.

### Requirement 8: Status contract

**User Story:** As a tenant, I want readable status fields that report the names
and URL of my provisioned resources, so that I can consume the AWS kit without
inspecting managed resources directly.

#### Acceptance Criteria

1. THE XRD SHALL enable the status subresource for the TenantEnvironment.
2. THE TenantEnvironment SHALL declare `status.bucketName` in the XRD status
   schema as a string that is not required on create.
3. THE TenantEnvironment SHALL declare `status.tableName` in the XRD status
   schema as a string that is not required on create.
4. THE TenantEnvironment SHALL declare `status.repositoryUrl` in the XRD status
   schema as a string that is not required on create.

### Requirement 9: Schema source and XRD consistency

**User Story:** As a platform engineer, I want the SimpleSchema source and the
hand-maintained XRD to describe the same API shape, so that the documented
schema and the enforced schema do not drift.

#### Acceptance Criteria

1. THE schema.yaml SHALL declare exactly the same set of `spec` fields as
   definition.yaml, with matching names and nesting, such that neither file
   contains a `spec` field absent from the other.
2. THE schema.yaml SHALL declare, for every `spec` field, the same type, enum
   value set, and default value as definition.yaml, where the compared
   attributes are: field type; the allowed enum values for `environment` (`dev`,
   `staging`, `prod`), `region`, and `table.billingMode` (`PAY_PER_REQUEST`,
   `PROVISIONED`); and the default values for `region` (`ap-southeast-1`),
   `bucket.versioning` (`true`), `table.enabled` (`false`), `table.hashKey`
   (`id`), and `repository.enabled` (`false`).
3. THE consistency comparison between schema.yaml and definition.yaml SHALL
   exclude only the CEL `x-kubernetes-validations` rules that SimpleSchema
   cannot express, namely the `tenant` name pattern and the immutability
   (`self == oldSelf`) rules on `tenant` and `environment`.
4. IF schema.yaml and definition.yaml disagree on any compared field name, type,
   enum value set, or default, THEN THE consistency check SHALL fail and report
   the diverging field, treating definition.yaml as the authoritative shape.
5. WHEN a change modifies the shared `spec` field shape in either schema.yaml or
   definition.yaml, THE same change SHALL update the other file so that criteria
   1 through 3 hold.

### Requirement 10: Example resources render against the schema

**User Story:** As a platform engineer, I want example `TenantEnvironment`
resources covering the enabled and disabled variants, so that the schema is
demonstrated and the render loop has valid inputs.

#### Acceptance Criteria

1. THE examples directory SHALL contain exactly one `TenantEnvironment` example
   with `apiVersion: platform.hello-crossplane.io/v1alpha1` and
   `kind: TenantEnvironment` in which both `spec.table.enabled` and
   `spec.repository.enabled` are set to `false`.
2. THE examples directory SHALL contain exactly one `TenantEnvironment` example
   with `apiVersion: platform.hello-crossplane.io/v1alpha1` and
   `kind: TenantEnvironment` in which both `spec.table.enabled` and
   `spec.repository.enabled` are set to `true`, and `spec.table.hashKey` and
   `spec.table.billingMode` are present.
3. THE examples SHALL set `metadata.name` to `<tenant>-<env>` and
   `metadata.namespace` to the identical `<tenant>-<env>` string, where
   `<tenant>` matches `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$` and `<env>` is one of
   `dev`, `staging`, or `prod`.
4. THE examples SHALL each set the required `spec.tenant` and `spec.environment`
   fields, with the `<tenant>-<env>` value equal to `metadata.name`.
5. WHEN `crossplane composition render` is run against each example file in
   `examples/tenantenvironments/`, THE render command SHALL complete with a zero
   exit code and produce no schema-validation error.
