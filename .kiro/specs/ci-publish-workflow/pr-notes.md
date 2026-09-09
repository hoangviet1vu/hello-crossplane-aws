# PR Notes — CI Publish Workflow

## Summary of changes

The workflow `.github/workflows/ci.yaml` already existed and satisfied the bulk
of the requirements. This change adds the two fail-fast guards called out in the
design's [Divergences / open decisions](design.md#divergences--open-decisions),
and documents the divergences that are satisfied structurally rather than by
added code.

- **D1 — CLI version guard (R7.3 / R7.4), added to both the `check` and
  `publish-dev` jobs.** Around each Crossplane CLI install step:
  - Before install, a guard exits non-zero if `XP_VERSION` is unset or empty
    (`[ -n "${XP_VERSION}" ] || { echo "XP_VERSION is unset/empty"; exit 1; }`),
    so the run stops before the Quality_Gate or any publish step (R7.3).
  - After install, `crossplane version` output is matched against `${XP_VERSION}`
    and the run exits non-zero on mismatch, catching `install.sh` silently falling
    back to a default when the pinned version is unavailable (R7.4).
  - The guard wording is identical in both jobs so the two CLI installs behave the
    same way.

- **D4 — empty-`Example_Set` guard (R3.6), added to the "Render every example"
  step.** The render step now enables `nullglob`, collects
  `examples/tenantenvironments/*.yaml` into an array, and exits non-zero with a
  clear "Example_Set is empty" message when the array is empty. The existing
  `::group::`-folded render loop then iterates the collected array rather than a
  bare glob, so a zero-match set fails the gate instead of passing silently
  (R3.5, R3.6).

## Structural divergences

The design reconciles the stricter refined acceptance criteria against the actual
implementation in its
[Divergences / open decisions](design.md#divergences--open-decisions) section.
Three of those items are satisfied *structurally* — by how the workflow is
shaped — and are intentionally left without an added runtime guard, per the
`product.md` principle of preferring the simplest thing that works end to end.
The labels below follow design.md; tasks.md task 8 references the same D2 / D3 /
D5 labels.

### D2 — repository-flag presence and sameness (R8.4 / R8.5 / R5.5)

`crossplane project build` and `crossplane project push` both hardcode
`--repository="${DEV_REPO}"`, reading the same single workflow-level variable. The
flag is therefore always present on both invocations and always byte-for-byte
identical *by construction*. A runtime equality check would compare a variable
against itself, so none is added. R8.3 / R8.4 / R8.5 / R5.5 are satisfied
structurally by the single-variable pattern. When the future tag job is added, it
will introduce its own single variable for the release repo and the same argument
carries over.

> Mapping note: the design's D2 is the "missing / mismatched `--repository`"
> divergence. This is the divergence tasks.md task 8 lists as
> "repository-flag sameness via the single `$DEV_REPO` variable." It is distinct
> from the deferred tagged-release job, which the design treats under D5 / R6.3
> (see below) and as future scope in the design's *Out of scope* subsection.

### D3 — `packages: write` permission (R10.2)

The workflow declares a top-level `permissions` block granting `packages: write`
(alongside `contents: read`). GitHub does not expose *effective* permissions as a
queryable pre-check inside a run, so there is no first-class way to assert R10.2
at runtime; inventing a token-introspection probe is out of scope for this PoC.
The declared `permissions` block is the mechanism, and the natural failure mode of
a missing grant is a push-time 403 — already covered by the GHCR-auth and
push-failure handling (R4.7 / R4.8). R10.2 is treated as satisfied by the
declaration.

> Mapping note: the design's D3 corresponds to tasks.md task 8's
> "`packages: write` satisfied by the declared top-level `permissions` block with
> no runtime pre-check."

### D5 — a `v*` tag must not push anywhere today (R6.3)

The workflow's `on:` list contains only `pull_request` and `push` to `main`. A tag
push is not a listed trigger, so the workflow does not run at all for a `v*` tag,
which trivially means it pushes to neither the Dev_Registry nor the
Release_Registry (R6.3). No change is needed. When the tagged-release flow is
added later, its publish job must target `RELEASE_REPO` and be gated on the tag
ref; the `DEV_REPO` / `RELEASE_REPO` split already present in `env` makes that a
mechanical addition (R6.1, R6.2). The dependency-cache preparation
(`actions/cache` keyed on `hashFiles('crossplane-project.yaml')` plus
`crossplane dependency update-cache`) is shared by the publish path and carries
over unchanged to the future tag job.

> Mapping note: the design's D5 corresponds to tasks.md task 8's "a `v*` tag does
> not trigger the workflow so it pushes nowhere today." This is the same
> divergence the tag job being deferred (only the main-merge path implemented)
> depends on; the design records the deferred tag job itself under its
> *Out of scope* subsection (reserved future scope per R6), and the D5 / R6.3
> guarantee is what keeps a stray tag safe in the meantime.

_Requirements covered by this section: 5.5, 6.2, 6.3, 8.3, 8.4, 8.5, 10.1, 10.2._

## Main-merge publish verification

The end-to-end publish path (GHCR login, `crossplane project build`,
`crossplane project push`, and the resulting `v0.0.0-<short-sha>` tag) can only be
verified in CI, on a real merge to `main`. It is not locally reproducible.

**Why it is CI-only / not locally verifiable**

- The push authenticates to GHCR with the built-in `GITHUB_TOKEN`, which only
  exists inside a GitHub Actions run. There is no equivalent credential locally,
  and using a personal access token would diverge from the workflow's actual auth
  path.
- The `publish-dev` job is gated by `if: github.event_name == 'push' && github.ref == 'refs/heads/main'`,
  so it runs only for a real push to `main` — never on a PR and never locally.
- There is no local GitHub Actions runner path that publishes to GHCR, so no
  local test should attempt to push a package.

**Expected image ref**

- `ghcr.io/hoangviet1vu/hello-crossplane-aws-dev` (the `DEV_REPO` snapshot
  registry — NOT the release repo `ghcr.io/hoangviet1vu/hello-crossplane-aws`).

**Expected tag format**

- `v0.0.0-<short-sha>` — a valid semver prerelease carrying the 7-character short
  commit SHA (e.g. `v0.0.0-a1b2c3d`). It matches `^v0\.0\.0-[0-9a-f]{7}$` and
  sorts below every real release, so a main snapshot never wins a version
  constraint against a tagged release.

**What a reviewer should check in the Actions run after merge**

1. The `check` job passed (quality gate green) before `publish-dev` started.
2. The **Log in to GHCR** step (`docker/login-action`) succeeded using
   `secrets.GITHUB_TOKEN`.
3. The **Build package** step ran `crossplane project build --repository=ghcr.io/hoangviet1vu/hello-crossplane-aws-dev`.
4. The **Push package** step ran `crossplane project push` with the **same**
   `--repository=ghcr.io/hoangviet1vu/hello-crossplane-aws-dev` and
   `--tag=v0.0.0-<short-sha>`, pushing the Configuration and embedded function
   package(s) together.
5. The resulting package landed in `ghcr.io/hoangviet1vu/hello-crossplane-aws-dev`
   (confirm on the GHCR package page) with a tag matching `^v0\.0\.0-[0-9a-f]{7}$`,
   and nothing was pushed to the release repo.
