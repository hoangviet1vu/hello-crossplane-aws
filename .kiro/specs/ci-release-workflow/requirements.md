# Requirements Document

## Introduction

This feature adds the tagged-release publish path to the existing single CI
workflow (`.github/workflows/ci.yaml`) for the `hello-crossplane-aws` Crossplane
Control Plane Project. When a Git tag matching `v*` is pushed, the workflow runs
the existing quality gate and then builds and publishes the Package to the
release GHCR repository, tagging the Package with the pushed Git tag name.

This is the flow reserved as future scope by the sibling `ci-publish-workflow`
spec (its Requirement 6). It is the counterpart to the already-implemented
main-merge snapshot flow: the main-merge flow publishes unreleased snapshots to
the `-dev` registry with a `v0.0.0-<short-sha>` version, while this flow publishes
real releases to the release registry using the Git tag as the version. The
registry split is fixed so that snapshots and releases never collide and version
resolution always prefers real releases.

This spec covers **only** the tag-triggered release job. It does not re-specify
the pull-request check-only behaviour or the main-merge/dev-snapshot publish flow,
both of which already exist. The new job is added to the same `ci.yaml` file — it
is not a new workflow file.

Versioning and registry rules are fixed by the steering documentation
(`.kiro/steering/tech.md#ci-and-versioning`, `.kiro/steering/product.md`) and are
treated as authoritative constraints in the requirements below.

## Glossary

- **CI_Workflow**: The single GitHub Actions workflow defined in
  `.github/workflows/ci.yaml`. It is the only workflow file in the repository.
- **Release_Job**: The job added to the CI_Workflow that runs when a Git tag
  matching `v*` is pushed, publishing the Package to the Release_Registry.
- **Quality_Gate**: The existing set of checks — `gofmt -s` formatting
  verification, `go vet ./...`, `go test ./...`, and `crossplane composition
  render` over every file in the Example_Set — that must pass before any Package
  is published.
- **Package**: The built Crossplane xpkg comprising the Configuration package and
  the embedded composition function package(s), published together.
- **Release_Registry**: `ghcr.io/hoangviet1vu/hello-crossplane-aws`, the GHCR
  repository for tagged releases.
- **Dev_Registry**: `ghcr.io/hoangviet1vu/hello-crossplane-aws-dev`, the GHCR
  repository for unreleased main-merge snapshots (owned by the sibling flow, out
  of scope here except as a non-target for this job).
- **Release_Tag**: The Git tag that triggered the run, matching the pattern `v*`
  (e.g. `v0.1.0`).
- **Release_Version**: The Package tag used when publishing, which is the
  Release_Tag name itself and must be a valid semantic version.
- **Crossplane_CLI**: The `crossplane` command-line tool, pinned to a fixed
  version via the `XP_VERSION` environment variable.
- **Repository_Flag**: The `--repository` argument passed to both
  `crossplane project build` and `crossplane project push`.
- **Dependency_Cache**: The Crossplane dependency/model cache directory populated
  by `crossplane dependency update-cache`.
- **Example_Set**: Every YAML file under `examples/tenantenvironments/`.

## Requirements

### Requirement 1: Tag push triggers the release job

**User Story:** As a platform team member, I want pushing a `v*` Git tag to trigger a release publish, so that cutting a release is a single tag-push action.

#### Acceptance Criteria

1. WHEN a Git tag whose name matches the glob pattern `v*` is pushed, THE CI_Workflow SHALL start exactly one workflow run that includes the Release_Job.
2. WHEN a pull request event triggers the CI_Workflow, THE CI_Workflow SHALL NOT execute the Release_Job.
3. WHEN a push to the `main` branch triggers the CI_Workflow, THE CI_Workflow SHALL NOT execute the Release_Job.
4. IF a workflow run is triggered by a Git tag whose name does not match the glob pattern `v*`, THEN THE CI_Workflow SHALL NOT execute the Release_Job.
5. IF a workflow run is triggered by any event other than a push of a Git tag matching the glob pattern `v*`, THEN THE CI_Workflow SHALL NOT execute the Release_Job.

### Requirement 2: Quality gate precedes release publish

**User Story:** As a platform team member, I want the release job to publish only after the same quality gate passes, so that no release artifact is produced from a change that fails checks.

#### Acceptance Criteria

1. WHEN a `v*` tag push triggers the CI_Workflow, THE CI_Workflow SHALL run the Quality_Gate against the commit the `v*` tag references before the Release_Job builds or publishes the Package.
2. WHEN a `v*` tag push triggers the CI_Workflow AND all Quality_Gate checks pass, THE CI_Workflow SHALL proceed to build and publish the Package.
3. IF the Quality_Gate does not complete with all checks passing during a `v*` tag push run (any check fails, or the Quality_Gate is skipped or cancelled), THEN THE CI_Workflow SHALL NOT build or push the Package and SHALL complete with a failure status that indicates which Quality_Gate check failed or that the Quality_Gate did not pass.
4. THE Release_Job SHALL reuse the same Quality_Gate definition as the existing check job rather than defining a separate set of checks.

### Requirement 3: Release job builds then publishes

**User Story:** As a platform team member, I want the release job to build the package and then push it, so that a failed build never results in a published release.

#### Acceptance Criteria

1. WHEN the Release_Job runs on a `v*` tag push AND the Quality_Gate has passed, THE Release_Job SHALL refresh the Dependency_Cache, then build the Package, then push the Package, passing the Repository_Flag pointing to the Release_Registry explicitly to both the build and push operations and performing the push only after the build completes successfully.
2. WHEN the Release_Job pushes the Package, THE Release_Job SHALL push the Configuration package and the embedded function package(s) together in a single `crossplane project push`.
3. IF the build operation fails during the Release_Job, THEN THE CI_Workflow SHALL NOT push the Package to the Release_Registry and SHALL terminate the run with a failure status that indicates the build step failed, leaving the Release_Registry unchanged.
4. IF `crossplane project push` to the Release_Registry fails, THEN THE CI_Workflow SHALL terminate the run with a failure status and SHALL surface an error indicating the push failure.
5. IF refreshing the Dependency_Cache fails during the Release_Job, THEN THE CI_Workflow SHALL NOT proceed to the build or push operations and SHALL terminate the run with a failure status that indicates the dependency-cache refresh failed.

### Requirement 4: Publishing to the release registry

**User Story:** As a platform team member, I want tagged releases published to the release registry, so that real releases are isolated from dev snapshots.

#### Acceptance Criteria

1. WHEN a `v*` tag push triggers the Release_Job, THE Release_Job SHALL push the built Package to the Release_Registry `ghcr.io/hoangviet1vu/hello-crossplane-aws`.
2. WHEN the Release_Job invokes build and push, THE Release_Job SHALL pass the Repository_Flag set to the Release_Registry `ghcr.io/hoangviet1vu/hello-crossplane-aws` to both commands, overriding the `spec.repository` default declared in `crossplane-project.yaml`.
3. WHEN the Release_Job publishes a Package, THE Release_Job SHALL push only to the Release_Registry and SHALL NOT push to the Dev_Registry `ghcr.io/hoangviet1vu/hello-crossplane-aws-dev`.
4. WHEN the Release_Job resolves the target registry, THE Release_Job SHALL consume the existing `RELEASE_REPO` workflow environment constant rather than a new inline literal.
5. IF the `RELEASE_REPO` constant is missing or resolves to an empty value, THEN THE CI_Workflow SHALL terminate the Release_Job with a failure status before invoking build or push, surface an error indicating the release repository is undefined, and push nothing to any registry.

### Requirement 5: Release version is the git tag

**User Story:** As a platform team member, I want the published package version to be the Git tag name, so that the package version matches the release name and sorts as a real release.

#### Acceptance Criteria

1. WHEN the Release_Job pushes the Package, THE Release_Job SHALL set the Release_Version equal to the Release_Tag name (the value of `github.ref_name`) with no transformation, truncation, or prefix stripping.
2. WHERE the run is triggered by a Release_Tag, THE Release_Job SHALL treat the Release_Version as valid only when it conforms to the semantic versioning format (MAJOR.MINOR.PATCH with optional prerelease and build-metadata suffixes, e.g. `v0.1.0`), and SHALL reject any value containing only a bare commit SHA.
3. IF the Release_Tag is not a valid semantic version, THEN THE CI_Workflow SHALL fail the run before pushing the Package, SHALL leave the Release_Registry unchanged (no Package published for that run), and SHALL surface an error indicating the release version is an invalid semantic version.
4. IF the Release_Tag is empty or unavailable when the Release_Job starts, THEN THE CI_Workflow SHALL fail the run before pushing the Package and SHALL surface an error indicating the release version is missing.

### Requirement 6: Repository flag consistency

**User Story:** As a platform team member, I want the release registry passed explicitly and identically to build and push, so that embedded functions are referenced consistently in the Composition.

#### Acceptance Criteria

1. WHEN the Release_Job invokes `crossplane project build`, THE Release_Job SHALL pass a non-empty Repository_Flag explicitly.
2. WHEN the Release_Job invokes `crossplane project push`, THE Release_Job SHALL pass a non-empty Repository_Flag explicitly.
3. WHEN the Release_Job invokes both `crossplane project build` and `crossplane project push` within a single run, THE Release_Job SHALL supply a Repository_Flag value that is byte-for-byte identical between the two commands and equal to the Release_Registry `ghcr.io/hoangviet1vu/hello-crossplane-aws`.
4. IF the Repository_Flag supplied to either build or push is missing or empty, THEN THE CI_Workflow SHALL terminate the Release_Job with a failure status before invoking push, surface an error indicating the repository flag is required, and push nothing to any registry.
5. IF the Repository_Flag value supplied to build differs from the value supplied to push within the same run, THEN THE CI_Workflow SHALL terminate the Release_Job with a failure status before invoking push, surface an error indicating the repository values must match, and push nothing to any registry.

### Requirement 7: Crossplane CLI version pinning

**User Story:** As a platform team member, I want the Crossplane CLI pinned in the release job, so that the beta project workflow behaves reproducibly when cutting a release.

#### Acceptance Criteria

1. WHEN the Release_Job installs the Crossplane_CLI, THE Release_Job SHALL install the exact version specified by the `XP_VERSION` environment variable.
2. THE Release_Job SHALL consume the same fixed explicit `XP_VERSION` value used by the existing jobs and SHALL NOT use a floating tag or the `latest` tag.
3. IF the `XP_VERSION` environment variable is unset or empty when the Release_Job attempts to install the Crossplane_CLI, THEN THE CI_Workflow SHALL report a failing status within the CLI-install step, SHALL NOT proceed to run the Quality_Gate, and SHALL NOT publish any Package.
4. IF the installed Crossplane_CLI version does not match the value of `XP_VERSION`, THEN THE CI_Workflow SHALL report a failing status with an error indication reporting both the expected `XP_VERSION` value and the installed version, and SHALL NOT proceed to publish any Package.
5. THE CI_Workflow SHALL define `XP_VERSION` as a single workflow-level environment constant that every job installing the Crossplane_CLI consumes, so the Release_Job and existing jobs resolve to one identical value.

### Requirement 8: Dependency cache preparation

**User Story:** As a platform team member, I want dependencies refreshed and cached before the release build, so that release builds are correct and fast across runs.

#### Acceptance Criteria

1. WHEN the Release_Job prepares to build the Package, THE Release_Job SHALL run `crossplane dependency update-cache` before invoking `crossplane project build`.
2. WHEN the Release_Job caches the Dependency_Cache, THE Release_Job SHALL key the cache on the exact contents of `crossplane-project.yaml`, such that a run whose `crossplane-project.yaml` content is byte-for-byte identical to a prior run reuses the cached Dependency_Cache, and a run whose content differs by one or more bytes produces a cache miss and refreshes the Dependency_Cache.
3. WHILE a cache entry keyed on the current `crossplane-project.yaml` contents exists (a cache hit), THE Release_Job SHALL restore the Dependency_Cache to its stored location before running `crossplane dependency update-cache`.
4. IF `crossplane dependency update-cache` exits with a non-zero status, THEN THE Release_Job SHALL NOT invoke `crossplane project build`, SHALL terminate with a failure status, and SHALL surface an error reporting the dependency refresh failure.
5. WHEN `crossplane dependency update-cache` completes with a zero exit status, THE Release_Job SHALL persist the refreshed Dependency_Cache under the cache key derived from `crossplane-project.yaml` for reuse by subsequent runs.

### Requirement 9: GHCR authentication

**User Story:** As a platform team member, I want the release job to authenticate to GHCR with the built-in token, so that publishing needs no personal access token.

#### Acceptance Criteria

1. WHEN the Release_Job authenticates to the registry `ghcr.io`, THE Release_Job SHALL log in using username `github.actor` and the built-in `GITHUB_TOKEN`, and SHALL NOT require or consume a personal access token.
2. WHEN the Release_Job invokes push, THE Release_Job SHALL reuse the Docker credentials established during the prior GHCR login step rather than re-authenticating.
3. WHEN the Release_Job executes, THE Release_Job SHALL complete the GHCR login step before invoking any push command.
4. IF GHCR authentication fails, THEN THE CI_Workflow SHALL terminate the Release_Job with a failure status before invoking any push, surface an error indicating authentication failed, and push nothing to any registry.
5. IF the `GITHUB_TOKEN` credential is missing or empty at login, THEN THE CI_Workflow SHALL terminate the Release_Job with a failure status before invoking any push, surface an error indicating GHCR credentials are unavailable, and push nothing to any registry.

### Requirement 10: Workflow permissions, file, and environment

**User Story:** As a platform team member, I want the release job to run with the correct permissions and environment inside the single workflow file, so that it can publish to GHCR while keeping CI in one place.

#### Acceptance Criteria

1. WHEN the CI_Workflow runs, THE CI_Workflow SHALL declare a top-level permissions block granting `contents: read` and `packages: write` that covers the Release_Job.
2. THE CI_Workflow SHALL define the Release_Job within the existing `.github/workflows/ci.yaml` file, which SHALL remain the only workflow file under `.github/workflows/`.
3. WHEN the Release_Job runs, THE Release_Job SHALL execute on an `ubuntu-latest` runner with a Docker daemon that is available and reachable for build and render operations.
4. IF the Docker daemon is unavailable or unreachable when the Release_Job requires it, THEN THE Release_Job SHALL fail the affected step, surface an error indicating Docker is required, and push no Package to any registry.

### Requirement 11: Registry separation preserved

**User Story:** As a platform team member, I want the release job to keep the dev and release registries distinct, so that adding the release flow does not disturb the existing snapshot separation.

#### Acceptance Criteria

1. THE CI_Workflow SHALL treat the Dev_Registry `ghcr.io/hoangviet1vu/hello-crossplane-aws-dev` and the Release_Registry `ghcr.io/hoangviet1vu/hello-crossplane-aws` as two distinct, non-equal values.
2. THE CI_Workflow SHALL keep `spec.repository` in `crossplane-project.yaml` pointed at the Release_Registry `ghcr.io/hoangviet1vu/hello-crossplane-aws` as the local-publish default.
3. WHEN the Release_Job publishes a Package, THE Release_Job SHALL target only the Release_Registry and SHALL NOT modify the main-merge snapshot publishing path.
4. WHEN the Release_Job runs, THE Release_Job SHALL NOT target the Dev_Registry `ghcr.io/hoangviet1vu/hello-crossplane-aws-dev`.
5. WHEN the main-merge snapshot job runs, THE CI_Workflow SHALL NOT target the Release_Registry `ghcr.io/hoangviet1vu/hello-crossplane-aws`.
