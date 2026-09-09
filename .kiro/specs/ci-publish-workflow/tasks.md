# Implementation Plan: CI Publish Workflow

## Overview

The workflow `.github/workflows/ci.yaml` already exists and satisfies the bulk of
the requirements. Per the design's [Divergences / open decisions](design.md) the
only remaining code changes are two fail-fast guards:

- **D1 (R7.3/R7.4)** — assert `XP_VERSION` is set before install and that the
  installed CLI version matches it, in both jobs that install the CLI.
- **D4 (R3.6)** — fail the render step when the Example_Set is empty instead of
  silently passing.

The remaining divergences (D2, D3, D5) are satisfied structurally and need only
documentation, not code. The rest of the tasks validate the edited workflow with
the design's Testing Strategy — favouring `actionlint`, YAML parse, and local
shell-guard checks, since there is no local way to run a full main-merge publish.

## Tasks

- [x] 1. Add the CLI version guard (D1) to the `check` job
  - Before the CLI install step, add a guard that exits non-zero if `XP_VERSION`
    is unset or empty (e.g. `[ -n "${XP_VERSION}" ] || { echo "XP_VERSION is unset/empty"; exit 1; }`)
  - After the install, verify the installed CLI reports the pinned version by
    matching `crossplane version` output against `${XP_VERSION}`; exit non-zero on mismatch
  - Confirm the exact match string against real `crossplane version` output format
    (run `crossplane version` locally or inspect its output) before finalizing the `grep`
  - _Requirements: 7.1, 7.2, 7.3, 7.4_

- [x] 2. Add the same CLI version guard (D1) to the `publish-dev` job
  - Apply the identical unset/empty check and post-install version match around
    the CLI install step in `publish-dev`, keeping the guard wording consistent with task 1
  - _Requirements: 7.3, 7.4_

- [x] 3. Add the empty-Example_Set guard (D4) to the "Render every example" step
  - Enable `nullglob`, collect `examples/tenantenvironments/*.yaml` into an array,
    and exit non-zero with a clear message if the array is empty
  - Iterate the collected array (not a bare glob) so a zero-match set fails the
    gate rather than passing silently, preserving the existing `::group::` render loop
  - _Requirements: 3.5, 3.6_

- [x] 4. Checkpoint - validate the edited workflow
  - Run `actionlint` (or equivalent) and a YAML parse over `.github/workflows/ci.yaml`
    to confirm it is valid Actions syntax after the edits
  - Ensure the checks pass, ask the user if questions arise
  - _Requirements: 10.3_

- [x] 5. Locally verify the D1 guard fails when triggered
  - In a scratch copy, blank `XP_VERSION` and confirm the guard exits non-zero;
    then confirm a mismatched installed-version string is also rejected; revert the scratch copy
  - _Requirements: 7.3, 7.4_

- [x] 6. Locally verify the D4 guard fails when triggered
  - Point the guard's glob at an empty directory in a scratch copy and confirm it
    exits non-zero with the empty-Example_Set message; revert
  - _Requirements: 3.6_

- [x] 7. Static review of the gate-before-publish contract
  - Verify by inspection that `publish-dev` declares `needs: check` and the
    main-only `if: github.event_name == 'push' && github.ref == 'refs/heads/main'`,
    and that the `check` job contains no GHCR login or `project push` step
  - _Requirements: 1.2, 1.3, 2.5, 4.6, 5.2_

- [x] 8. Document the structural divergences (D2, D3, D5) in the PR description
  - Note that repository-flag sameness is guaranteed by the single `$DEV_REPO`
    variable (D2), `packages: write` is satisfied by the declared top-level
    `permissions` block with no runtime pre-check (D3), and a `v*` tag does not
    trigger the workflow so it pushes nowhere today (D5)
  - _Requirements: 5.5, 6.2, 6.3, 8.3, 8.4, 8.5, 10.1, 10.2_

- [x] 9. Document the main-merge publish verification (manual / CI-only)
  - After a real merge to `main`, confirm the pushed package tag matches
    `^v0\.0\.0-[0-9a-f]{7}$` and lands in `DEV_REPO`, not `RELEASE_REPO`
  - This step cannot be run locally (it requires a GHCR push from a main merge);
    record it as a manual post-merge check in the PR description
  - _Requirements: 4.1, 4.2, 5.1_

## Notes

- Tasks marked with `*` are optional verification/documentation steps and can be
  skipped for a faster MVP; tasks 1–4 are the required work.
- This feature is CI/CD configuration, so the design intentionally omits a
  Correctness Properties section — there are no property-based tests, only guard
  and validation checks.
- Task 9 is inherently manual: there is no local GitHub Actions runner path that
  publishes to GHCR, so do not invent tests that push packages.
- Each task references specific requirement clauses for traceability.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1"] },
    { "id": 1, "tasks": ["2"] },
    { "id": 2, "tasks": ["3"] },
    { "id": 3, "tasks": ["5", "6", "7", "8", "9"] }
  ]
}
```
