# ADR-0001: Pipeline runner — wrap Tekton

**Status:** Proposed

## Context
We need a container-step execution engine. Options: Tekton, fork Drone, write our own.

## Decision
Wrap Tekton Pipelines. Hoofprint compiles its own YAML into Tekton `PipelineRun`s and treats Tekton as an implementation detail users never touch.

## Why
- CNCF graduated, k8s-native, actively maintained.
- Users never see Tekton YAML, so its verbosity doesn't hurt the product.
- Avoids trademark and licensing entanglement with Harness-owned Drone.

## Consequences
- Hard dependency on Kubernetes (already true for v1).
- We own the spec → Tekton compiler; that's the core engineering of M1.
- Revisit if Tekton's install footprint makes the 30-minute quickstart impossible.
