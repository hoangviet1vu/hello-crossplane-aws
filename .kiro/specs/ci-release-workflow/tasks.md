# Implementation Plan: CI Release Workflow

## Overview

Add a single tag-triggered `publish-release` job to the existing
`.github/workflows/ci.yaml`. The job mirrors the existing `publish-dev`
main-merge snapshot job step-for-step, differing only on three axes: the trigger
(`v*` tag push), the registry (`RELEASE_REPO`), and the package version
(`github.ref_name`). All edits are confined to `ci.yaml` — it stays the only
workflow file. Existing workflow-level constants (`XP_VERSION`, `RELEASE_REPO`,
`DEV_REPO`, `FUNCTION_DIR`), the top-level `permissions` block, the
dependency-cache setup, GHCR login via `GITHUB_TOKEN`, and the explicit
`--repository` build/push pattern are all reused verbatim.

This is a declarative GitHub Actions workflow change, not Go code. The design's
Correctness Properties section is intentionally omitted (see design.md), so there
are no property-based test sub-tasks. Verification is example-based and
execution-based: the trigger truth table, semver accept/reject examples, and a
release rehearsal.

## Tasks

- [x] 1. Add the `v*` tag filter to the push trigger
  - Edit the `on:` block in `.github/workflows/ci.yaml` so the existing `push`
    trigger gains `tags: ['v*']` alongside the existing `branches: [main]`,
    leaving the `pull_request` trigger unchanged.
  - Confirm a `v*` tag push starts exactly one workflow run and that the existing
    `check` and `publish-dev` behaviour is untouched.
  - _Requirements: 1.1, 10.2, 11.3_

- [x] 2. Add the gated `publish-release` job skeleton
  - [x] 2.1 Declare the `publish-release` job with its gate and runner
    - Add a new `publish-release` job to `ci.yaml` with `needs: check`,
      `runs-on: ubuntu-latest`, and the `if` guard
      `github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')`.
    - This makes the job wait for the full Quality_Gate, run only on `v*` tag
      pushes, and never run on pull requests, `main` pushes, non-`v` tags, or
      other events — keeping it mutually exclusive with `publish-dev`.
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 2.1, 2.2, 2.3, 2.4, 10.1, 10.3, 11.3, 11.5_

- [x] 3. Add the release-job setup steps (checkout, Go, CLI pin)
  - [x] 3.1 Add Checkout and Go setup steps
    - Add `actions/checkout@v4` and `actions/setup-go@v5` with
      `go-version-file: ${{ env.FUNCTION_DIR }}/go.mod`, matching `publish-dev`.
    - _Requirements: 10.3_

  - [x] 3.2 Add the Crossplane CLI install-and-verify step
    - Reuse the existing install-and-verify script: fail if `XP_VERSION` is
      unset/empty before installing, install via the pinned `install.sh`, then
      verify the installed client version equals `XP_VERSION`, failing with a
      message naming expected vs actual otherwise.
    - Consume the single workflow-level `XP_VERSION` constant; no floating or
      `latest` tag. Because this precedes build/push, a pin failure publishes
      nothing.
    - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.5_

- [x] 4. Add the semver validation guard step
  - [x] 4.1 Validate `github.ref_name` before login/build/push
    - Add a guard step that reads `github.ref_name`, fails if empty/unavailable,
      fails if it is not a valid semantic version (rejecting a bare SHA, `latest`,
      and incomplete versions like `v1`/`v1.2`) with an "invalid semantic
      version" error, and on success exports the validated value unchanged (no
      transformation, truncation, or prefix stripping) for the push step.
    - Placing this before build/push ensures an invalid tag leaves the registry
      unchanged.
    - _Requirements: 5.1, 5.2, 5.3, 5.4_

- [x] 5. Add the GHCR login step
  - [x] 5.1 Log in to GHCR with the built-in token before any push
    - Add `docker/login-action@v3` with `registry: ghcr.io`,
      `username: ${{ github.actor }}`, `password: ${{ secrets.GITHUB_TOKEN }}`,
      ordered before build/push so a missing/empty token or auth failure fails
      before anything is published; the later push reuses these Docker
      credentials (no PAT, no re-auth).
    - _Requirements: 9.1, 9.2, 9.3, 9.4, 9.5_

- [x] 6. Add the dependency-cache steps
  - [x] 6.1 Add cache restore and `update-cache` steps
    - Add `actions/cache@v4` with `path: ~/.crossplane/cache` and
      `key: crossplane-cache-${{ hashFiles('crossplane-project.yaml') }}` plus a
      `restore-keys` prefix fallback, then `crossplane dependency update-cache`
      before build. A byte-identical project file hits the cache; any change
      misses. A non-zero `update-cache` exit fails the run before build/push; a
      zero exit persists the refreshed cache under the key.
    - _Requirements: 8.1, 8.2, 8.3, 8.4, 8.5, 3.5_

- [x] 7. Add the build and push steps targeting the release registry
  - [x] 7.1 Add the build step with explicit `--repository`
    - Add `crossplane project build --repository="${RELEASE_REPO}"`, consuming
      the existing `RELEASE_REPO` constant (not a new literal), pointing at the
      release registry. A build failure fails the run before push, leaving the
      registry unchanged.
    - _Requirements: 3.1, 3.3, 4.2, 4.4, 4.5, 6.1, 11.2_

  - [x] 7.2 Add the push step with matching `--repository` and the tag
    - Add `crossplane project push --repository="${RELEASE_REPO}"
      --tag="<validated ref_name>"`, running only after a successful build. The
      `--repository` value reuses the same `RELEASE_REPO` reference as build so
      the two are byte-for-byte identical. A single `project push` ships the
      Configuration and embedded function package(s) together, targets only the
      release registry (never the dev registry), and surfaces a push failure.
    - _Requirements: 3.1, 3.2, 3.4, 4.1, 4.3, 5.1, 6.2, 6.3, 6.4, 6.5, 11.1, 11.4_

- [x] 8. Checkpoint - static validation
  - Confirm `ci.yaml` remains valid GitHub Actions YAML and the only file under
    `.github/workflows/`, that the `check` and `publish-dev` jobs are unchanged
    except for the shared `on:`/`env` edits, and that the top-level `permissions`
    block still grants `contents: read` and `packages: write`. Ask the user if
    questions arise.
  - _Requirements: 10.1, 10.2, 11.2_

- [x] 9. Verify trigger gating against the truth table
  - [x] 9.1 Review the `on:` block and `publish-release` `if` guard
    - Walk each row of the design trigger truth table (push tag `v0.1.0` → runs;
      pull request → no; push to `main` → no; push tag `nightly` → no;
      `workflow_dispatch`/other → no) and confirm mutual exclusion with
      `publish-dev`.
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 11.3, 11.5_

- [x] 10. Verify semver validation behaviour
  - [x] 10.1 Exercise the validate step against representative inputs
    - Run the guard fragment locally: accept `v0.1.0`, `v1.2.3`, `v0.1.0-rc.1`,
      `v0.1.0+build.5`; reject empty string, a bare short SHA, `latest`, and
      incomplete `v1`/`v1.2`.
    - _Requirements: 5.1, 5.2, 5.3, 5.4_

- [x] 11. Verify the end-to-end release path
  - [x] 11.1 Dry local build and release rehearsal
    - Run `crossplane project build --repository=<release repo>` locally to
      confirm the build succeeds with an explicit repository flag (do not push
      from a workstation). Then push a disposable prerelease tag (e.g.
      `v0.0.1-test.1`) and confirm the release job runs after `check`, the
      package lands in the release registry tagged with the exact tag name, and
      nothing lands in the `-dev` registry. Delete the test tag and package
      afterward.
    - _Requirements: 3.1, 4.1, 4.3, 5.1, 6.1, 11.4_

## Notes

- Tasks marked with `*` are optional verification sub-tasks and can be skipped for
  a faster MVP; the core workflow edits (tasks 1–7) are not optional.
- Each task references the specific requirements it satisfies for traceability.
- No property-based test tasks: the deliverable is a declarative workflow, and the
  design's Correctness Properties section is intentionally omitted with
  justification.
- Verification is example-based and execution-based (trigger truth table, semver
  examples, release rehearsal), consistent with a CI-configuration change.
- All edits are confined to `.github/workflows/ci.yaml`; no new workflow file is
  created and the existing `check`/`publish-dev` jobs are left unchanged apart
  from shared `on:`/`env` blocks.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1", "2.1"] },
    { "id": 1, "tasks": ["3.1", "3.2", "4.1", "5.1", "6.1"] },
    { "id": 2, "tasks": ["7.1"] },
    { "id": 3, "tasks": ["7.2"] },
    { "id": 4, "tasks": ["9.1", "10.1"] },
    { "id": 5, "tasks": ["11.1"] }
  ]
}
```
