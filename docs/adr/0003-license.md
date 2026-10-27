# ADR-0003: License — Apache-2.0

**Status:** Proposed

## Decision
Core under Apache-2.0. Enterprise features (if any) in a separate, clearly marked module under a commercial license.

## Why
- Matches Tekton, Argo, Kubernetes; no friction for CNCF-adjacent contributors.
- Maximizes early adoption, which matters more than cloud-wrapping risk at this stage.

## Alternatives rejected
- AGPL: protects against hyperscaler wrapping but scares enterprise legal.
- BSL: the Drone/Harness precedent is exactly what we're positioning against.
