# Requirements Document

## Introduction

This feature defines the continuous integration and publishing pipeline for the
`hello-crossplane-aws` Crossplane Control Plane Project. A single GitHub Actions
workflow (`.github/workflows/ci.yaml`) runs quality-gate checks on every change
and publishes the built package to GitHub Container Registry (GHCR) on merges to
`main`.

The pipeline serves the platform team: it guarantees that every change passes
formatting, static analysis, unit tests, and composition rendering before any
artifact is published, and it produces traceable snapshot packages for the
proof-of-concept. Publishing on merge targets a dedicated `-dev` snapshot
registry; a future tagged-release flow targeting the release registry is a
non-goal for this spec but the registry split is fixed so it can be added
mechanically later.

Versioning and registry rules are fixed by the steering documentation
(`.kiro/steering/tech.md#ci-and-versioning`) and are treated as authoritative
constraints in the requirements below.

## Glossary

- **CI_Workflow**: The single GitHub Actions workflow defined in
  `.github/workflows/ci.yaml`. It is the only workflow in the repository.
- **Quality_Gate**: The set of checks — `gofmt -s` formatting verification,
  `go vet ./...`, `go test ./...`, and `crossplane composition render` over every
  example — that must pass before any package is published.
- **Package**: The built Crossplane xpkg comprising the Configuration package and
  the embedded composition function package(s), published together.
- **Dev_Registry**: `ghcr.io/hoangviet1vu/hello-crossplane-aws-dev`, the GHCR
  repository for unreleased main-merge snapshots.
- **Release_Registry**: `ghcr.io/hoangviet1vu/hello-crossplane-aws`, the GHCR
  repository for tagged releases (future flow, out of scope here).
- **Short_SHA**: The abbreviated Git commit hash of the merge commit on `main`.
- **Snapshot_Version**: The package tag `v0.0.0-<Short_SHA>`, a valid semver
  prerelease that sorts below every real release.
- **Crossplane_CLI**: The `crossplane` command-line tool, pinned to a fixed
  version via the `XP_VERSION` environment variable.
- **Repository_Flag**: The `--repository` argument passed to both
  `crossplane project build` and `crossplane project push`.
- **Dependency_Cache**: The Crossplane dependency/model cache directory populated
  by `crossplane dependency update-cache`.
- **Example_Set**: Every YAML file under `examples/tenantenvironments/`.

## Requirements

### Requirement 1: Pull request runs checks only

**User Story:** As a platform team member, I want pull requests to run only the quality-gate checks, so that broken changes are caught before merge without publishing any artifact.

#### Acceptance Criteria

1. WHEN a pull request event triggers the CI_Workflow, THE CI_Workflow SHALL run the Quality_Gate consisting of a `gofmt -s` formatting check, `go vet`, `go test`, and a composition render over every example in the Example_Set.
2. WHEN a pull request event triggers the CI_Workflow, THE CI_Workflow SHALL NOT push any Package to the Dev_Registry.
3. WHEN a pull request event triggers the CI_Workflow, THE CI_Workflow SHALL NOT push any Package to the Release_Registry.
4. IF any Quality_Gate check fails during a pull request run, THEN THE CI_Workflow SHALL terminate the run without executing any subsequent publish step and SHALL report a failing completion status for that run that indicates which Quality_Gate check failed.

### Requirement 2: Merge to main runs checks then publishes

**User Story:** As a platform team member, I want merges to `main` to publish a snapshot package after the checks pass, so that every merged change produces a traceable artifact.

#### Acceptance Criteria

1. WHEN a push to the `main` branch triggers the CI_Workflow, THE CI_Workflow SHALL run the Quality_Gate.
2. WHEN a push to the `main` branch triggers the CI_Workflow AND all Quality_Gate checks pass, THE CI_Workflow SHALL refresh the Dependency_Cache, then build the Package, then push the Package, performing the push only after the build completes successfully.
3. WHEN the CI_Workflow builds or pushes the Package for a push to the `main` branch, THE CI_Workflow SHALL pass the Repository_Flag explicitly to both the build operation and the push operation with an identical value targeting the Dev_Registry.
4. WHEN the CI_Workflow pushes the Package for a push to the `main` branch, THE CI_Workflow SHALL tag the Package with a Snapshot_Version of the form `v0.0.0-<Short_SHA>`.
5. IF any Quality_Gate check fails during a push to the `main` branch, THEN THE CI_Workflow SHALL NOT build or push the Package and SHALL complete with a failure status that indicates which Quality_Gate check failed.
6. IF the build operation fails during a push to the `main` branch, THEN THE CI_Workflow SHALL NOT push the Package to the Dev_Registry and SHALL complete with a failure status that indicates the build failed.

### Requirement 3: Quality gate checks

**User Story:** As a platform team member, I want a defined set of quality checks to run on every change, so that formatting, static analysis, tests, and composition rendering are all verified consistently.

#### Acceptance Criteria

1. WHEN the CI_Workflow runs the Quality_Gate, THE CI_Workflow SHALL check that every Go source file in the module at functions/compose-tenant-environment is `gofmt -s` formatted.
2. IF one or more Go source files in the module at functions/compose-tenant-environment are not `gofmt -s` formatted, THEN THE CI_Workflow SHALL fail the Quality_Gate and SHALL report the set of unformatted files.
3. WHEN the CI_Workflow runs the Quality_Gate, THE CI_Workflow SHALL run `go vet ./...` over the module at functions/compose-tenant-environment, and IF `go vet` reports one or more problems, THEN THE CI_Workflow SHALL fail the Quality_Gate.
4. WHEN the CI_Workflow runs the Quality_Gate, THE CI_Workflow SHALL run `go test ./...` over the module at functions/compose-tenant-environment, and IF one or more tests fail, THEN THE CI_Workflow SHALL fail the Quality_Gate.
5. WHEN the CI_Workflow runs the Quality_Gate, THE CI_Workflow SHALL run `crossplane composition render` once for each file in the Example_Set, and IF the render of any file in the Example_Set fails, THEN THE CI_Workflow SHALL fail the Quality_Gate and SHALL report the failing file.
6. IF the Example_Set contains zero files WHEN the CI_Workflow runs the Quality_Gate, THEN THE CI_Workflow SHALL fail the Quality_Gate.

### Requirement 4: Publishing on main merge

**User Story:** As a platform team member, I want main-merge publishes to go to the dev snapshot registry with a commit-traceable version, so that snapshots are isolated from real releases and every snapshot is traceable to its commit.

#### Acceptance Criteria

1. WHILE the Quality_Gate has passed for the current run, WHEN the CI_Workflow publishes on a push to `main`, THE CI_Workflow SHALL push the Package to the Dev_Registry, passing the Repository_Flag pointing to the Dev_Registry explicitly to both `crossplane project build` and `crossplane project push` so it overrides `spec.repository`.
2. WHEN the CI_Workflow publishes on a push to `main`, THE CI_Workflow SHALL tag the Package with the Snapshot_Version `v0.0.0-<Short_SHA>`, where Short_SHA is the first 7 characters of the commit SHA.
3. WHEN the CI_Workflow publishes on a push to `main`, THE CI_Workflow SHALL push the Configuration package and the embedded function package(s) together in a single `crossplane project push`.
4. WHEN the CI_Workflow authenticates to GHCR, THE CI_Workflow SHALL use the built-in `GITHUB_TOKEN`.
5. WHEN the CI_Workflow pushes the Package, THE CI_Workflow SHALL reuse the Docker credentials established at GHCR login.
6. IF the Quality_Gate has not passed for the current run, THEN THE CI_Workflow SHALL NOT push the Package to the Dev_Registry.
7. IF authentication to GHCR with the built-in `GITHUB_TOKEN` fails, THEN THE CI_Workflow SHALL terminate the run with a failure status, surface an error indicating the authentication failure, and SHALL NOT push the Package to any registry.
8. IF the `crossplane project push` to the Dev_Registry fails, THEN THE CI_Workflow SHALL terminate the run with a failure status, surface an error indicating the push failure, and SHALL NOT push the Package to the Release_Registry.

### Requirement 5: Fixed versioning and registry rules

**User Story:** As a platform team member, I want the registry and version for each trigger to be fixed, so that snapshot and release artifacts never collide and version resolution always prefers real releases.

#### Acceptance Criteria

1. WHEN the trigger is a push to `main`, THE CI_Workflow SHALL set the Repository_Flag to the Dev_Registry and use the Snapshot_Version `v0.0.0-<Short_SHA>`.
2. WHEN the trigger is a pull request, THE CI_Workflow SHALL NOT push a Package to any registry.
3. THE CI_Workflow SHALL use a Snapshot_Version that is a valid semantic-version prerelease that sorts below every release version of the form `vX.Y.Z` (X, Y, Z each in the range 0 to 999,999,999).
4. WHEN the trigger is a push to `main`, THE CI_Workflow SHALL pass an identical Repository_Flag value to both the build step and the push step.
5. IF the Repository_Flag value passed to the build step differs from the Repository_Flag value passed to the push step, THEN THE CI_Workflow SHALL halt without pushing a Package and SHALL surface an error indicating a Repository_Flag mismatch.

### Requirement 6: Tagged release flow is future scope

**User Story:** As a platform team member, I want the tagged-release flow reserved for later while keeping the registry split fixed, so that adding it is a mechanical change that respects the existing separation.

#### Acceptance Criteria

1. WHERE the Release_Registry target is defined for the future tagged-release flow, THE CI_Workflow SHALL set it to `ghcr.io/hoangviet1vu/hello-crossplane-aws` and SHALL set the future version source to the triggering tag name.
2. THE CI_Workflow SHALL define the Dev_Registry reference and the Release_Registry reference as two separate, non-equal target values, such that no single change to one alters the other.
3. IF a tag matching `v*` triggers the CI_Workflow before the tagged-release flow is implemented, THEN THE CI_Workflow SHALL NOT push any Package to the Dev_Registry or the Release_Registry.

### Requirement 7: Crossplane CLI version pinning

**User Story:** As a platform team member, I want the Crossplane CLI pinned to a fixed version, so that the beta project workflow behaves reproducibly across CI runs.

#### Acceptance Criteria

1. WHEN the CI_Workflow installs the Crossplane_CLI, THE CI_Workflow SHALL install the exact version specified by the `XP_VERSION` environment variable.
2. THE CI_Workflow SHALL set `XP_VERSION` to a fixed explicit semantic-version value and SHALL NOT set it to a floating or `latest` tag.
3. IF the `XP_VERSION` environment variable is unset or empty when the CI_Workflow attempts to install the Crossplane_CLI, THEN THE CI_Workflow SHALL report a failing status and SHALL NOT proceed to run the Quality_Gate or publish any Package.
4. IF the installed Crossplane_CLI version does not match the value of `XP_VERSION`, THEN THE CI_Workflow SHALL report a failing status and SHALL NOT proceed to run the Quality_Gate or publish any Package.

### Requirement 8: Repository flag consistency

**User Story:** As a platform team member, I want the target repository passed explicitly and identically to build and push, so that embedded functions are referenced consistently in the Composition.

#### Acceptance Criteria

1. WHEN the CI_Workflow invokes `crossplane project build`, THE CI_Workflow SHALL pass the Repository_Flag explicitly as a non-empty `--repository` argument on that invocation.
2. WHEN the CI_Workflow invokes `crossplane project push`, THE CI_Workflow SHALL pass the Repository_Flag explicitly as a non-empty `--repository` argument on that invocation.
3. WHEN the CI_Workflow invokes `crossplane project build` and `crossplane project push` within one workflow run, THE CI_Workflow SHALL pass a byte-for-byte identical Repository_Flag value to both invocations.
4. IF the CI_Workflow invokes `crossplane project build` or `crossplane project push` without an explicit Repository_Flag argument, THEN THE CI_Workflow SHALL report a failing status for that run and SHALL NOT push the Package.
5. IF the Repository_Flag value passed to `crossplane project push` differs from the Repository_Flag value passed to `crossplane project build` in the same workflow run, THEN THE CI_Workflow SHALL report a failing status for that run and SHALL NOT push the Package.

### Requirement 9: Dependency cache preparation

**User Story:** As a platform team member, I want dependencies refreshed and cached before build, so that builds are correct and fast across runs.

#### Acceptance Criteria

1. WHEN the CI_Workflow prepares to build the Package, THE CI_Workflow SHALL run `crossplane dependency update-cache` before invoking `crossplane project build`.
2. WHEN the CI_Workflow caches the Dependency_Cache, THE CI_Workflow SHALL key the cache on the exact contents of `crossplane-project.yaml`, such that a run whose `crossplane-project.yaml` content is byte-for-byte identical to a prior run reuses the cached Dependency_Cache, and a run whose `crossplane-project.yaml` content differs by one or more bytes produces a cache miss and refreshes the Dependency_Cache.
3. IF `crossplane dependency update-cache` exits with a non-zero status, THEN THE CI_Workflow SHALL not invoke `crossplane project build`, SHALL terminate with a failure status, and SHALL surface an error indication reporting the dependency refresh failure.

### Requirement 10: Workflow permissions and environment

**User Story:** As a platform team member, I want the workflow granted exactly the permissions and environment it needs, so that it can publish to GHCR and build packages while remaining the single source of CI truth.

#### Acceptance Criteria

1. THE CI_Workflow SHALL declare a top-level `permissions` block granting `packages: write`.
2. IF the CI_Workflow's effective permissions do not include `packages: write`, THEN THE CI_Workflow SHALL fail before any Package build or push step executes and SHALL surface an error indicating the missing GHCR write permission.
3. THE CI_Workflow SHALL be the only workflow file present under `.github/workflows/`, and it SHALL be located at the path `.github/workflows/ci.yaml`.
4. WHEN the CI_Workflow builds the embedded function or renders a composition, THE CI_Workflow SHALL run on a runner where the Docker daemon is available and reachable.
5. IF the Docker daemon is unavailable or unreachable when a function build or composition render step begins, THEN THE CI_Workflow SHALL fail that step and SHALL surface an error indicating Docker is required.
