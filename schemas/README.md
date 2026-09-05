# `schemas/`

**Generated code. Do not hand-edit.** Everything under this folder is produced
by the Crossplane CLI and regenerated on demand — any manual change is lost the
next time the models are regenerated.

## What this is

`schemas/` holds **typed API models** for the APIs this project knows about,
emitted in several languages. The Crossplane CLI reads the API schemas (this
project's own XRD, plus any provider/configuration dependencies) and generates
strongly-typed representations so composition code can build and read resources
against real types instead of hand-assembled unstructured maps.

The models here are the `dev.crossplane.io/models` module — the same module the
embedded Go composition function imports for typed access (see
`.kiro/steering/tech.md`).

## Why it exists

Using a typed Go function is the whole point of this project: naming rules,
length limits, and conditional resources are logic, and logic belongs in code
with tests. Typed models are what make that ergonomic and safe:

- Compile-time field names and types instead of stringly-typed maps.
- The `fn.go` scaffold converts collected resources to SDK types against these
  models.
- Steering rule: **use the generated models rather than building unstructured
  objects by hand.** If a type is missing, add the dependency and regenerate —
  never hand-edit a model.

## How it is generated

`crossplane dependency add` (and the CLI's dependency handling generally)
generates these models; `crossplane dependency update-cache` refreshes the cache
they are built from.

```bash
# Adds a dependency AND regenerates the models under schemas/.
crossplane dependency add xpkg.crossplane.io/crossplane-contrib/provider-aws-s3
crossplane dependency update-cache
```

Because the models are derived from the API schemas, changing `definition.yaml`
or adding a provider dependency and regenerating will change the contents here.

## Layout

| Path          | What it is                                                         |
| ------------- | ------------------------------------------------------------------ |
| `.lock.json`  | Records the hash of each source package the models were built from |
| `go/`         | Go models — module `dev.crossplane.io/models` (imported by `fn.go`)|
| `json/`       | JSON Schema for each kind, plus an `index.schema.json`             |
| `kcl/`        | KCL models                                                         |
| `python/`     | Python models (a `models` package with `pyproject.toml`)           |

Each language tree currently contains the `TenantEnvironment` types (this
project's own API, group `platform.hello-crossplane.io/v1alpha1`) and the
Kubernetes `meta/v1` types they depend on.

> Note: `.lock.json` currently references only `fs://apis` (this project's local
> XRD). Once AWS provider dependencies are added with `crossplane dependency
> add`, their managed-resource models (Bucket, Table, Repository, …) will be
> generated here too and the lock file will list them.

## Rules

- **Never hand-edit** anything under `schemas/`. Regenerate instead.
- The Go models carry a `DO NOT EDIT` header — respect it.
- To add a missing type, add the dependency (`crossplane dependency add …`) and
  regenerate; do not add types by hand.
