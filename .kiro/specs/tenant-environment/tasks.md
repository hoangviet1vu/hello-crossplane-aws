# Implementation Plan: TenantEnvironment API / Schema Definition

## Overview

This slice ships the `TenantEnvironment` API contract only — three declarative
YAML artifacts plus a small Go test harness that guards them:

- `apis/tenantenvironments/schema.yaml` — SimpleSchema source, kept as
  documentation, with a header comment marking `definition.yaml` authoritative
  and warning that `xrd generate` must not be re-run.
- `apis/tenantenvironments/definition.yaml` — the XRD
  (`apiextensions.crossplane.io/v2`, `spec.scope: Namespaced`), hand-maintained,
  carrying the OpenAPI schema for `spec`+`status`, the tenant OpenAPI `pattern`,
  the CEL `self == oldSelf` immutability rules, and the status subresource.
- `examples/tenantenvironments/*.yaml` — one disabled variant and one
  fully-enabled variant.

The embedded composition function (`fn.go`, `naming.go`), `composition.yaml`, and
provider dependencies are **out of scope** and get no tasks here. The build
approach is: write `definition.yaml` by hand (the generate-once step is a
one-time historical action, not a repeatable task), keep `schema.yaml` in sync as
documentation, add examples, then guard everything with a Go test module.

The Go test harness uses `pgregory.net/rapid` for the two property-based tests
(the design allows Go here because these tests parse YAML/regex and never import
Crossplane types).

## Tasks

- [x] 1. Author the XRD (`definition.yaml`) as the authoritative API
  - [x] 1.1 Create the XRD identity and version block
    - Create `apis/tenantenvironments/definition.yaml` with
      `apiVersion: apiextensions.crossplane.io/v2`, `kind: CompositeResourceDefinition`
    - Set group `platform.hello-crossplane.io`, `names.kind: TenantEnvironment`,
      `names.plural: tenantenvironments`
    - Define exactly one version `v1alpha1` with `served: true` and
      `referenceable: true`
    - Set `spec.scope: Namespaced`; include no `claimNames` field
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6_

  - [x] 1.2 Define the `spec` OpenAPI schema with types, enums, and defaults
    - Declare `spec.tenant` (string, required) and `spec.environment`
      (string enum `dev`/`staging`/`prod`, required)
    - Add OpenAPI `pattern: ^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$` on `spec.tenant`
    - Declare `spec.region` (string enum `ap-southeast-1`, `ap-southeast-2`,
      `ap-northeast-1`, `us-east-1`; default `ap-southeast-1`)
    - Declare nested `spec.bucket.versioning` (bool, default `true`)
    - Declare nested `spec.table`: `enabled` (bool, default `false`),
      `hashKey` (string, default `id`), `billingMode` (enum
      `PAY_PER_REQUEST`/`PROVISIONED`, default `PAY_PER_REQUEST`)
    - Declare nested `spec.repository.enabled` (bool, default `false`)
    - Mark `tenant` and `environment` as the only required fields
    - _Requirements: 2.1, 2.3, 4.1, 4.2, 5.1, 5.2, 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 7.1, 7.2_

  - [x] 1.3 Add the CEL immutability rules on identity fields
    - Add `x-kubernetes-validations` on `spec.tenant`: rule `self == oldSelf`,
      message `tenant is immutable`
    - Add `x-kubernetes-validations` on `spec.environment`: rule
      `self == oldSelf`, message `environment is immutable`
    - _Requirements: 3.1, 3.2_

  - [x] 1.4 Enable the status subresource and declare status fields
    - Enable the status subresource for the XR
    - Declare `status.bucketName`, `status.tableName`, `status.repositoryUrl`
      as strings, none required
    - _Requirements: 8.1, 8.2, 8.3, 8.4_

- [x] 2. Author `schema.yaml` as documentation, consistent with the XRD
  - [x] 2.1 Write the SimpleSchema source mirroring the XRD shape
    - Create `apis/tenantenvironments/schema.yaml` declaring the same `spec`
      field set, nesting, types, enums, and defaults as `definition.yaml`
      (region enum, `bucket.versioning`, `table.*`, `repository.enabled`)
    - Declare the same `status` fields (`bucketName`, `tableName`,
      `repositoryUrl`)
    - Add a header comment: `definition.yaml` is authoritative; do NOT re-run
      `crossplane xrd generate` (it deletes the CEL rules and tenant pattern);
      keep both files in sync by hand
    - _Requirements: 9.1, 9.2, 9.3, 9.5_

- [x] 3. Author the example resources
  - [x] 3.1 Create the disabled variant example
    - Create `examples/tenantenvironments/acme-dev.yaml` with
      `apiVersion: platform.hello-crossplane.io/v1alpha1`,
      `kind: TenantEnvironment`
    - Set `spec.table.enabled: false` and `spec.repository.enabled: false`
    - Set `metadata.name` and `metadata.namespace` both to `acme-dev`, with
      `spec.tenant: acme` and `spec.environment: dev`
    - _Requirements: 10.1, 10.3, 10.4_

  - [x] 3.2 Create the fully-enabled variant example
    - Create `examples/tenantenvironments/globex-prod.yaml` with the same
      apiVersion/kind
    - Set `spec.table.enabled: true`, `spec.repository.enabled: true`, and
      include `spec.table.hashKey` and `spec.table.billingMode`
    - Set `metadata.name` and `metadata.namespace` both to `globex-prod`, with
      `spec.tenant: globex` and `spec.environment: prod`
    - _Requirements: 10.2, 10.3, 10.4_

- [x] 4. Set up the Go guard-test module
  - [x] 4.1 Initialize the test module and dependencies
    - Create a self-contained Go module under `tests/` (e.g.
      `tests/schema/`) with `go.mod`
    - Add `pgregory.net/rapid` and a YAML parser (`sigs.k8s.io/yaml` or
      `gopkg.in/yaml.v3`); no Crossplane imports
    - Add a small loader helper that reads and parses `definition.yaml` and
      `schema.yaml` into comparable maps
    - _Requirements: 9.1, 9.4_

- [x] 5. Property-based tests
  - [x] 5.1 Property test — tenant name pattern accept/reject
    - Compile `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$`; generate valid strings and
      invalid strings (uppercase, leading/trailing hyphen, length < 3, length
      > 22, empty, illegal characters) with `rapid`
    - Assert the regex accepts exactly the valid set; minimum 100 iterations
    - Tag: `Feature: tenant-environment, Property 1: Tenant name pattern partitions all strings correctly`
    - **Property 1: Tenant name pattern partitions all strings correctly**
    - **Validates: Requirements 2.2**

  - [x] 5.2 Property test — schema.yaml <-> definition.yaml parity
    - Parse both files, walk the union of `spec` fields, assert name/nesting/
      type/enum-set/default agreement, excluding the CEL rules; randomize
      field-visit order; minimum 100 iterations
    - Optionally mutate a copy to confirm the check fails on injected drift
    - Tag: `Feature: tenant-environment, Property 2: schema.yaml and definition.yaml agree on the API shape`
    - **Property 2: schema.yaml and definition.yaml agree on the API shape**
    - **Validates: Requirements 9.1, 9.2, 9.3, 9.4, 9.5**

- [x] 6. Static / structural assertion tests
  - [x] 6.1 Assert XRD identity and status structure
    - Table-driven test on `definition.yaml`: `apiVersion`
      `apiextensions.crossplane.io/v2`, group `platform.hello-crossplane.io`,
      Kind/plural `TenantEnvironment`/`tenantenvironments`, one `v1alpha1`
      version with `served` and `referenceable` true, `spec.scope: Namespaced`,
      no `claimNames`, status subresource enabled, three status fields declared
      and not required
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 8.1, 8.2, 8.3, 8.4_

  - [x] 6.2 Assert CEL rules present with exact messages
    - Assert the two `self == oldSelf` rules exist with messages
      `tenant is immutable` and `environment is immutable` (regression guard
      against an accidental `xrd generate` re-run)
    - _Requirements: 3.1, 3.2_

  - [x] 6.3 Assert example shape
    - Assert exactly one disabled and one enabled example exist; each has
      `metadata.name == metadata.namespace == <tenant>-<env>` and
      `spec.tenant`/`spec.environment` consistent with that name
    - _Requirements: 10.1, 10.2, 10.3, 10.4_

- [x] 7. Checkpoint — validate the artifacts
  - Run `gofmt -l`, `go vet ./...`, `go test ./...` in the test module and
    confirm all guard tests pass
  - Validate each example against the XRD OpenAPI schema via `kubectl apply
    --dry-run=server` (XRD installed) or an offline OpenAPI validator. NOTE: the
    `crossplane composition render` acceptance gate (Req 10.5) depends on a
    `composition.yaml` from the out-of-scope function slice; do not block on it
    here
  - Ensure all tests pass, ask the user if questions arise.

## Notes

- Tasks marked with `*` are optional (the two property-based tests) and can be
  skipped for a faster MVP; the static assertion tests in task 6 are not
  optional because they are the core regression guard for the hand-maintained
  XRD.
- `definition.yaml` is authoritative and hand-maintained. Do not re-run
  `crossplane xrd generate` — it deletes the CEL rules and the tenant pattern.
- Out of scope: `fn.go`, `naming.go`, `composition.yaml`, provider dependencies,
  and anything below the API server. No tasks target them.
- Req 10.5 (`composition render` exits zero) depends on the out-of-scope
  function slice's `composition.yaml`; until then examples are validated via
  OpenAPI/dry-run, so no task here depends on the function.
- Region enum follows the design decision: `ap-southeast-1` (default),
  `ap-southeast-2`, `ap-northeast-1`, `us-east-1`.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "3.1", "3.2", "4.1"] },
    { "id": 1, "tasks": ["1.2"] },
    { "id": 2, "tasks": ["1.3", "1.4"] },
    { "id": 3, "tasks": ["2.1"] },
    { "id": 4, "tasks": ["5.1", "5.2", "6.1", "6.2", "6.3"] }
  ]
}
```
