# ADR-0002: Build our own UI rather than embedding Argo CD's

**Status:** Proposed

## Decision
Hoofprint ships its own React UI over hoofprint-api. Argo CD's UI is not exposed to end users by default.

## Why
The product thesis is "one pane." Linking out to Argo, Tekton Dashboard and Grafana reproduces exactly the fragmentation we're replacing.

## Consequences
- Significant frontend work in M1–M3.
- We must surface Argo sync status and errors faithfully; otherwise users will go around us.
