---
inclusion: always
---

# Product

## What this is

`hello-crossplane-aws` is a **Crossplane Control Plane Project** that publishes a
single self-service API, `TenantEnvironment`, for a multi-tenant PaaS. It turns a
dozen lines of tenant-authored YAML into real AWS infrastructure, without the
tenant needing to know how it is built.

This is a **proof of concept**. Prefer the simplest thing that works end to end
over completeness. Do not add features that were not asked for.

## The core API

One `TenantEnvironment` provisions, for one tenant in one environment:

- An **S3 bucket** with versioning — always created.
- A **DynamoDB table** — optional, gated by `spec.table.enabled` (default `false`).
- An **ECR repository** — optional, gated by `spec.repository.enabled` (default `false`).

Tenants describe *what* they want; an embedded Go composition function decides
*how* it is built. The XRD becomes a real Kubernetes API endpoint, giving
OpenAPI-validated schema, RBAC, versioning, readable status conditions, and
continuous reconciliation that corrects manual drift — all without writing a
controller.

## Who it serves

PaaS tenants who need self-service AWS kit, and the platform team who publishes
the API. Tenants interact through YAML and Kubernetes RBAC; the platform team
owns the composition logic and the package lifecycle.

## Why a control plane, not Terraform modules

Terraform still owns the substrate — accounts, VPCs, and the cluster Crossplane
runs in. Crossplane owns the per-tenant kit. A control plane gives a validated
API endpoint plus a reconcile loop that keeps AWS matching the spec, which
Terraform modules alone do not.

## Out of scope

Do not add any of the following unless explicitly asked:

- IAM roles or policies
- Connection secrets
- KMS encryption
- ArgoCD / GitOps wiring
- A tenant portal
- Multi-region
- Cost allocation tags or tags beyond `tenant` / `environment` / `managed-by`
- Tenant offboarding automation

IAM is the first thing to add *after* the PoC, but only when asked.
