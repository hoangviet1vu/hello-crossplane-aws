# Design Document

## Overview

This slice defines the `TenantEnvironment` self-service API for the
`hello-crossplane-aws` control plane. The deliverable is the **API contract**:
the schema a PaaS tenant may declare and the validation the Kubernetes API
server enforces. It is deliberately *definition only* — the embedded Go
composition function that turns a `TenantEnvironment` into AWS managed resources
is follow-on work and out of scope here. This design reserves the status fields
that function will populate, but does not design the function.

Three artifacts are produced:

| Artifact | Path | Role |
| --- | --- | --- |
| SimpleSchema source | `apis/tenantenvironments/schema.yaml` | Human-readable source of the API shape; kept as documentation after first generation. |
| XRD (the real API) | `apis/tenantenvironments/definition.yaml` | `apiextensions.crossplane.io/v2` CompositeResourceDefinition. Generated once from the schema, then hand-maintained to carry CEL rules SimpleSchema cannot express. |
| Example XRs | `examples/tenantenvironments/*.yaml` | Two sample `TenantEnvironment` resources (one minimal, one fully enabled) that exercise the schema and feed `crossplane composition render`. |

The design's central tension is that **the XRD is generated once and then
hand-maintained**. `crossplane xrd generate --from simpleschema` produces an
OpenAPI schema but not CEL `x-kubernetes-validations`. Re-running it silently
deletes the tenant name pattern and the immutability rules. So `definition.yaml`
becomes the authoritative source of truth, `schema.yaml` is retained only as
documentation, and the two must be kept consistent by hand. This shapes the
whole approach: the artifacts are simple YAML, but the *process* around them is
where correctness is won or lost.

Addresses: Requirements 1–10.

## Architecture

### Where this slice sits

```
Tenant-authored YAML  (a TenantEnvironment CR)
        │  kubectl apply -n <tenant>-<env>
        ▼
Kubernetes API Server
   ├─ OpenAPI schema validation   ← generated from schema.yaml, lives in definition.yaml
   └─ CEL x-kubernetes-validations ← hand-added to definition.yaml (pattern + immutability)
        │  accepted resource persisted, status subresource enabled
        ▼
   status.{bucketName, tableName, repositoryUrl}   ← reserved contract, populated later
        ┆
        ┆ (OUT OF SCOPE for this slice)
        ▼
   compose-tenant-environment  →  AWS managed resources
```

This slice ends at the API server. Everything below the dotted line — the
Composition, the embedded function, the managed resources — is follow-on work.
The only obligation this slice has toward it is to declare the three status
fields so the function has a stable place to write results (Requirement 8).

### Authoring workflow (generate-once, then hand-maintain)

```
schema.yaml ──(crossplane xrd generate --from simpleschema, ONCE)──▶ definition.yaml
                                                                          │
                        hand-add CEL: tenant pattern + immutability ──────┤
                                                                          ▼
                                                             definition.yaml (source of truth)

Thereafter:
  - Shape change?  Edit BOTH files by hand, keep them consistent (Req 9).
  - NEVER re-run `xrd generate` — it deletes the CEL rules (tech.md: "Regenerating is destructive").
```

This is the load-bearing architectural decision. It is documented here, in
`schema.yaml` as a header comment, and enforced by the consistency check
described in the Testing Strategy.

### Why namespaced, no claims

Crossplane v2 namespaced XRs replace the v1 claim/XR split. A `TenantEnvironment`
is itself namespaced (`spec.scope: Namespaced`), lives in the `<tenant>-<env>`
namespace, and is named `<tenant>-<env>`. There is no `claimNames` block. This
is a v2 invariant from the steering files, not a choice to revisit here.
(Requirements 1.1, 1.5, 1.6.)

## Components and Interfaces

### Component 1: `schema.yaml` — SimpleSchema source (documentation)

The SimpleSchema dialect that the XRD was generated from. It expresses field
names, types, enums, and defaults, but **cannot** express the tenant regex
pattern or the immutability rules. After first generation it serves as readable
documentation of the API shape and as one half of the consistency contract
(Requirement 9).

Interface (the fields it declares — see Data Models for full detail):
`tenant`, `environment`, `region`, `bucket.versioning`, `table.enabled`,
`table.hashKey`, `table.billingMode`, `repository.enabled`, plus the `status`
fields.

It carries a header comment stating that `definition.yaml` is authoritative and
that `xrd generate` must not be re-run.

### Component 2: `definition.yaml` — the XRD (authoritative)

The real API. An `apiextensions.crossplane.io/v2` CompositeResourceDefinition
with:

- **Identity** (Requirement 1): `group: platform.hello-crossplane.io`,
  `names.kind: TenantEnvironment`, `names.plural: tenantenvironments`, one
  version `v1alpha1` with `served: true` and `referenceable: true`,
  `spec.scope: Namespaced`, and no `claimNames`.
- **OpenAPI schema** for `spec` and `status` (Requirements 2, 4, 5, 6, 7, 8):
  types, enums, and defaults for every field.
- **CEL `x-kubernetes-validations`** (Requirements 2.2, 3): the tenant name
  pattern on `spec.tenant` and the `self == oldSelf` immutability rules on
  `spec.tenant` and `spec.environment`.
- **Status subresource enabled** (Requirement 8.1) with `bucketName`,
  `tableName`, `repositoryUrl` declared and not required.

The tenant pattern is expressed as an OpenAPI `pattern` where possible; the
immutability transition rules require CEL because they compare `self` to
`oldSelf`, which OpenAPI validation cannot do.

### Component 3: `examples/tenantenvironments/*.yaml` — sample XRs

Two files (Requirement 10):

- **Minimal / disabled variant** (e.g. `acme-dev.yaml`): `table.enabled: false`
  and `repository.enabled: false` (may be set explicitly or left to default).
  Exercises the "S3 only" path.
- **Fully enabled variant** (e.g. `globex-prod.yaml`): `table.enabled: true`,
  `repository.enabled: true`, with `table.hashKey` and `table.billingMode`
  present. Exercises every field.

Each sets `metadata.name` and `metadata.namespace` to the same `<tenant>-<env>`
string, and `spec.tenant` / `spec.environment` consistent with that name.

### Interface summary: the validation contract

The API server is the enforcement point. The externally observable contract:

| Input condition | API server behavior | Requirement |
| --- | --- | --- |
| `spec.tenant` matches pattern, `environment` in enum | accept | 2.1, 2.3 |
| `spec.tenant` violates pattern | reject, no partial resource | 2.2 |
| `environment` outside enum, or `tenant`/`environment` missing | reject | 2.4 |
| update changes `tenant` or `environment` | reject, persisted value unchanged | 3.3, 3.4 |
| update leaves both identity fields equal | accept | 3.5 |
| create with any schema-valid identity values | accept (no immutability applied) | 3.6 |
| `region` omitted | default `ap-southeast-1` | 4.2 |
| `region` outside allow-list | reject | 4.3 |
| `bucket.versioning` omitted | default `true` | 5.2 |
| `table.*` omitted | defaults: `enabled=false`, `hashKey=id`, `billingMode=PAY_PER_REQUEST` | 6.2, 6.4, 6.6 |
| `table.billingMode` outside enum | reject | 6.7 |
| `repository.enabled` omitted | default `false` | 7.2 |
| resource created without a namespace | reject | 1.9 |

## Data Models

### `spec` schema

| Field | Type | Required | Default | Constraint | Requirement |
| --- | --- | --- | --- | --- | --- |
| `tenant` | string | yes | — | pattern `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$` (3–22 chars); **immutable** (CEL) | 2.1, 2.2, 3.1, 3.3 |
| `environment` | string (enum) | yes | — | `dev` \| `staging` \| `prod`; **immutable** (CEL) | 2.3, 3.2, 3.4 |
| `region` | string (enum) | no | `ap-southeast-1` | supported-region allow-list (see decision below) | 4.1, 4.2, 4.3 |
| `bucket.versioning` | bool | no | `true` | — | 5.1, 5.2 |
| `table.enabled` | bool | no | `false` | — | 6.1, 6.2 |
| `table.hashKey` | string | no | `id` | — | 6.3, 6.4 |
| `table.billingMode` | string (enum) | no | `PAY_PER_REQUEST` | `PAY_PER_REQUEST` \| `PROVISIONED` | 6.5, 6.6, 6.7 |
| `repository.enabled` | bool | no | `false` | — | 7.1, 7.2 |

`bucket`, `table`, and `repository` are nested objects. Their sub-fields carry
the defaults above so that a tenant may supply an empty (or absent) `table`
block and still get a well-formed, defaulted structure.

### `status` schema (reserved contract only)

| Field | Type | Required | Requirement |
| --- | --- | --- | --- |
| `bucketName` | string | no | 8.2 |
| `tableName` | string | no | 8.3 |
| `repositoryUrl` | string | no | 8.4 |

Status subresource enabled (8.1). These fields are declared but unpopulated in
this slice — the composition function writes them later.

### Design decision: the supported-region allow-list

The requirements reference a "supported-region allow-list" whose members were
left open (Requirement 4, Glossary). The steering fixes only `ap-southeast-1` as
the default. Proposed minimal, SE-Asia-focused enum:

```
ap-southeast-1   (Singapore)      ← default
ap-southeast-2   (Sydney)
ap-northeast-1   (Tokyo)
us-east-1        (N. Virginia)
```

Rationale: `ap-southeast-1` is the fixed default and anchors the region to SE
Asia; `ap-southeast-2` and `ap-northeast-1` are the nearest APAC neighbours for
latency/DR; `us-east-1` is included as the canonical global default many tenants
expect and where some AWS services first land. This is a PoC-sized list and is
**adjustable** — it lives only in the `region` enum in both `schema.yaml` and
`definition.yaml`, so changing it is a two-file edit. Flagging for user input:
the exact membership is a policy call, not a technical constraint.

### External naming (context only, not built here)

The `tenant` pattern (3–22 chars, lowercase alphanumeric + hyphens, no leading/
trailing hyphen) is the intersection of the S3, DynamoDB, and ECR naming rules,
so a name that passes the schema is valid for all three downstream resources.
Rejecting a bad tenant name at the API server (Requirement 2.2) is cheaper than
provisioning two resources and failing on the third. The actual external-name
derivation (`<tenant>-<env>-bucket`, etc.) belongs to the out-of-scope function.

## Correctness Properties

*A property is a characteristic or behavior that should hold true across all
valid executions of a system — essentially, a formal statement about what the
system should do. Properties serve as the bridge between human-readable
specifications and machine-verifiable correctness guarantees.*

### Acceptance Criteria Testing Prework

This slice ships **declarative YAML artifacts** (an XRD, a SimpleSchema source,
example CRs), not code with pure functions. That shapes the classification:
most criteria describe *structural invariants of the artifacts* or *API-server
validation behavior*, both of which are verified by inspecting the committed
files or by running `crossplane composition render` / a live-cluster apply —
not by property-based tests over a function under test.

Two areas are genuinely amenable to property-based testing because they have a
large input space and a "for all inputs" statement:

1. **Tenant-name validation (Req 2.2):** the pattern
   `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$` partitions all strings into accept /
   reject. A generator can produce matching and non-matching strings and assert
   the boundary — this is `error-conditions` + `invariant` PBT against the
   compiled pattern.
2. **Schema/XRD consistency (Req 9):** the field-set, types, enums, and defaults
   in `schema.yaml` and `definition.yaml` must agree (excluding the CEL rules).
   This is a `model-based` / parity property comparing two parsed structures.

Everything else is classified INTEGRATION (needs a live API server or `render`)
or EXAMPLE/SMOKE (structural assertion on a single artifact).

```
1.1–1.6  XRD structural identity (apiVersion, group, Kind/plural, version
         served+referenceable, Namespaced scope, no claimNames)
  Thoughts: Fixed structural assertions on definition.yaml. One inspection
  answers each; input does not vary. Not universal-over-inputs.
  Classification: EXAMPLE (static assertion on the XRD)
1.7      XRD reaches Established=True within 60s
  Thoughts: Requires a live cluster; tests kube-apiserver + Crossplane, not our
  artifact logic. Bounded liveness.
  Classification: INTEGRATION
1.8      API server serves the resource once Established
  Thoughts: Live-cluster behavior gated on 1.7.
  Classification: INTEGRATION
1.9      Reject namespace-less create
  Thoughts: API-server behavior for a namespaced resource; live cluster (or
  dry-run apply).
  Classification: INTEGRATION
2.1,2.3  tenant required string; environment required enum
  Thoughts: Structural schema assertions.
  Classification: EXAMPLE
2.2      Reject tenant not matching pattern; accept matching
  Thoughts: Large string input space, clear accept/reject boundary, tests the
  pattern itself. Behavior varies meaningfully with input. 100+ generated
  strings find edge cases (leading/trailing hyphen, length bounds, uppercase).
  Classification: PROPERTY
2.4      Reject missing/invalid identity fields
  Thoughts: A few concrete negative cases; not a broad input space.
  Classification: EDGE_CASE (exercised via examples + render/apply)
3.1,3.2  CEL rules present in definition.yaml with exact messages
  Thoughts: Static assertion the rule strings exist.
  Classification: EXAMPLE
3.3,3.4  Reject mutation of tenant/environment on update
  Thoughts: CEL transition rule; requires an actual create-then-update against
  an API server. Cannot be exercised by static inspection or render.
  Classification: INTEGRATION
3.5      Accept update leaving identity unchanged
  Thoughts: Live-cluster update behavior.
  Classification: INTEGRATION
3.6      Create accepts any schema-valid identity (no immutability on create)
  Thoughts: Live-cluster create behavior; overlaps 2.2 acceptance side.
  Classification: INTEGRATION
4.2,5.2,6.2,6.4,6.6,7.2  Defaulting of omitted fields
  Thoughts: Deterministic defaults applied by the API server from the OpenAPI
  `default:` keyword. Behavior does not vary with input (absent -> fixed value).
  Verified by applying a minimal resource and reading it back.
  Classification: INTEGRATION (server-side apply) / EXAMPLE (default present in XRD)
4.1,4.3,4.4,5.1,5.3,6.1,6.5,6.7,7.1,7.3  Enum/type accept-reject
  Thoughts: Small fixed enums / type checks, not a large input space worth 100
  iterations. Best shown with representative accept + reject examples.
  Classification: EDGE_CASE
8.1–8.4  Status subresource enabled; status fields declared, not required
  Thoughts: Static structural assertions on the XRD.
  Classification: EXAMPLE
9.1–9.4  schema.yaml <-> definition.yaml parity (fields, types, enums, defaults)
  Thoughts: Two structures that must agree across every field, excluding CEL.
  A parity property over the union of fields holds "for all fields". Catches
  drift that a hand-picked example would miss.
  Classification: PROPERTY
9.5      A shape change to one file updates the other
  Thoughts: A process obligation; testable as the 9.1–9.4 invariant on the
  committed state (subsumed by the parity property).
  Classification: PROPERTY (subsumed by 9.1–9.4)
10.1–10.4  Example files exist with the required field values / name==namespace
  Thoughts: Static assertions on two specific files.
  Classification: EXAMPLE
10.5     `crossplane composition render` exits zero for each example
  Thoughts: Synchronous command with observable exit code; tests the artifacts
  end-to-end but with fixed example inputs (2 files), not a generated space.
  Classification: INTEGRATION (the primary acceptance gate for this slice)
```

**Property reflection.** After classification, only two PROPERTY candidates
remain, and they are non-redundant: one covers *input validation* (tenant
strings, Req 2.2), the other covers *artifact parity* (schema vs XRD, Req 9).
Neither implies the other. The 9.5 "keep them in sync" obligation is fully
subsumed by the 9.1–9.4 parity property evaluated on the committed files, so it
is not a separate property. No further consolidation is possible or needed.

### Property 1: Tenant name pattern partitions all strings correctly

*For any* string `s`, the tenant validation SHALL accept `s` if and only if `s`
matches `^[a-z0-9]([a-z0-9-]{1,20})[a-z0-9]$` (equivalently: length 3–22, all
characters lowercase-alphanumeric or hyphen, no leading or trailing hyphen). A
generator produces both matching strings and deliberately invalid strings
(uppercase, leading/trailing hyphen, too short, too long, empty, embedded
illegal characters) and the accept/reject decision must agree with the pattern.

**Validates: Requirements 2.2**

### Property 2: schema.yaml and definition.yaml agree on the API shape

*For any* `spec` field present in either `schema.yaml` or `definition.yaml`, the
field SHALL appear in both with the same name, nesting, type, enum value set,
and default value — comparing exactly the attributes named in Requirement 9.2
and excluding only the CEL `x-kubernetes-validations` rules (the tenant pattern
and the `self == oldSelf` immutability rules) that SimpleSchema cannot express.
If any field diverges on name, type, enum set, or default, the property fails
and reports the diverging field, treating `definition.yaml` as authoritative.

**Validates: Requirements 9.1, 9.2, 9.3, 9.4, 9.5**

## Error Handling

Error handling in this slice is entirely **declarative rejection at the API
server** — there is no imperative code path to catch exceptions in. The design's
job is to make the API server reject bad input rather than let a malformed
`TenantEnvironment` reach the (out-of-scope) composition function.

| Failure | Mechanism | Observable result | Requirement |
| --- | --- | --- | --- |
| Invalid tenant name | OpenAPI `pattern` on `spec.tenant` | 422 rejection, no partial resource, pattern error | 2.2 |
| Missing required field | OpenAPI `required` | 422 rejection | 2.1, 2.4 |
| Out-of-enum `environment` / `region` / `billingMode` | OpenAPI `enum` | 422 rejection with enum error | 2.4, 4.3, 6.7 |
| Wrong type (non-bool, non-string) | OpenAPI `type` | 422 rejection | 4.4, 5.3, 6.7, 7.3 |
| Mutated `tenant` / `environment` on update | CEL `self == oldSelf` | 422 rejection, persisted value unchanged, immutability message | 3.3, 3.4 |
| Namespace-less create | Namespaced scope + API-server behavior | request rejected, no resource retained | 1.9 |

Design principles for error handling here:

- **Reject at the edge.** A bad tenant name is caught by the API server, not
  discovered when the third AWS resource fails to provision. The `tenant`
  pattern is the intersection of S3/DynamoDB/ECR naming rules precisely so a
  single pattern check protects all three.
- **No partial persistence.** Every rejection leaves no resource behind
  (Requirements 2.2, 4.3), which is the API server's native create-validation
  behavior — the design relies on it rather than adding logic.
- **Immutability needs CEL, not OpenAPI.** OpenAPI cannot compare a new value to
  the old one, so `self == oldSelf` transition rules are the only mechanism for
  Requirements 3.3–3.4. These are the rules `xrd generate` would delete, hence
  the hand-maintained `definition.yaml`.
- **Clear messages.** The immutability rules carry the exact messages
  `tenant is immutable` and `environment is immutable` (Requirements 3.1, 3.2)
  so the rejection is self-explanatory in `kubectl` output and XR events.

## Testing Strategy

Testing is scaled to a **PoC, schema-only slice**. The bulk of the contract is
declarative YAML, so the primary gate is `crossplane composition render` over
the examples plus static assertions on the artifacts. Two lightweight
property-based tests cover the areas with a real input space.

### Primary gate: render over every example (Requirement 10.5)

The definition of done requires `crossplane composition render` to succeed for
**every** file in `examples/tenantenvironments/`, including the disabled variant.
This is the end-to-end acceptance check for the slice and runs in CI on every PR
(no cluster, no AWS):

```bash
crossplane composition render \
  examples/tenantenvironments/acme-dev.yaml \
  apis/tenantenvironments/composition.yaml
```

> Note: `render` needs a `composition.yaml`. That file is produced by the
> out-of-scope function slice. Until it exists, the example-validation portion
> of Requirement 10.5 is exercised by schema-validating each example against the
> XRD's OpenAPI schema (e.g. `kubectl apply --dry-run=server` on a cluster with
> the XRD installed, or an offline OpenAPI validator). This dependency is called
> out so the sequencing between slices is explicit.

### Property-based tests (2 properties)

A small, self-contained test module (language flexible — Go fits the repo, but
these tests do not touch Crossplane types, so any harness works). Use an
established PBT library — **do not hand-roll generators**:

- Go: `pgregory.net/rapid` or `testing/quick`
- If done in another language, its standard PBT library.

Each property test runs **minimum 100 iterations** and is tagged with a comment
referencing the design property:

- **Property 1 — tenant pattern.** Generate valid strings (from the pattern) and
  invalid strings (uppercase, leading/trailing hyphen, length < 3 or > 22,
  empty, illegal characters). Assert the compiled regex accepts exactly the
  valid set.
  Tag: `Feature: tenant-environment, Property 1: Tenant name pattern partitions all strings correctly`
- **Property 2 — schema/XRD parity.** Parse both `schema.yaml` and
  `definition.yaml`, walk the union of `spec` fields, and assert name/type/enum/
  default agreement, excluding the CEL rules. Randomize field-visit order and
  (optionally) mutate a copy to confirm the check *fails* on injected drift.
  Tag: `Feature: tenant-environment, Property 2: schema.yaml and definition.yaml agree on the API shape`

### Static / example assertions (structural invariants)

Lightweight checks on the committed artifacts, runnable in CI without a cluster.
These cover the EXAMPLE-classified criteria:

- **XRD identity (Req 1.1–1.6, 8.1–8.4):** assert `definition.yaml` has
  `apiVersion: apiextensions.crossplane.io/v2`, group
  `platform.hello-crossplane.io`, Kind/plural `TenantEnvironment`/
  `tenantenvironments`, one `v1alpha1` version with `served` and `referenceable`
  true, `spec.scope: Namespaced`, no `claimNames`, status subresource enabled,
  and the three status fields declared and not required. A `yq`/`grep` script or
  a table-driven unit test suffices.
- **CEL rules present (Req 3.1, 3.2):** assert the two `self == oldSelf` rules
  and their exact messages exist. This doubles as a regression guard against an
  accidental `xrd generate` re-run wiping them.
- **Example shape (Req 10.1–10.4):** assert exactly one disabled and one enabled
  example exist, with `metadata.name == metadata.namespace == <tenant>-<env>`
  and matching `spec.tenant`/`spec.environment`.

### Integration tests (live cluster — out of CI's PR path)

The INTEGRATION-classified criteria need a real API server and are validated
during local `crossplane project run`, not on every PR (they are slow and need a
cluster). One representative example each, not exhaustive:

- XRD establishes within 60s and serves the resource (Req 1.7, 1.8).
- Namespace-less create is rejected (Req 1.9).
- Create-then-update mutating `tenant`/`environment` is rejected with the
  immutability message; an unchanged update is accepted (Req 3.3–3.5).
- Defaulting: apply a minimal resource, read it back, confirm `region`,
  `bucket.versioning`, `table.*`, `repository.enabled` defaults (Req 4.2, 5.2,
  6.2, 6.4, 6.6, 7.2).

### Why not more PBT

Enum and type checks (regions, `billingMode`, booleans) have tiny fixed input
spaces — representative accept/reject examples find every bug that 100 random
iterations would, at lower cost. Defaulting and immutability are API-server
behaviors, not pure functions, so they belong in integration tests. Per the PBT
guidance, testing the Kubernetes API server or Crossplane itself is out of
scope; we test our artifacts, not the platform.
