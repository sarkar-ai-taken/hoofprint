# Harness migration track

Goal: a Harness customer runs one command and sees their pipelines in Hoofprint.

## Importer: `hoofprint import harness`
Input: Harness pipeline YAML (exported via Harness API or Git-synced pipelines).
Output: `hoofprint.yaml` + a migration report listing what mapped, what was approximated, and what needs manual work.

### Mapping
| Harness concept        | Hoofprint concept                 | Notes |
|------------------------|-----------------------------------|-------|
| Pipeline               | Pipeline                          | 1:1 |
| Stage (CI)             | Stage, steps as containers        | Run/Plugin steps map directly |
| Stage (Deployment)     | Deploy stage                      | k8s only in v1; others flagged |
| Service                | Service (new first-class object)  | add to spec |
| Environment / Infra def| Environment                       | 1:1 |
| Connector              | Connector                         | Git, registry, k8s, Prom, Vault |
| Secret                 | Secret reference                  | 1:1 |
| Trigger                | Trigger                           | 1:1 |
| Approval stage         | Approval stage (v2)               | flagged in report until built |
| Template               | Template (v2)                     | inlined on import, flagged |
| Policy (OPA)           | Policy gate (v2)                  | flagged |
| Continuous Verification| Verify stage                      | Prom queries carry over |
| Delegate               | hoofprint-agent                   | 1:1, selectors → tags |

## Phasing
- **M1**: importer for CI stages + triggers + connectors. Report flags everything else.
- **M2**: deploy stages for k8s. Service object added to spec.
- **v2 (post-launch)**: approvals, templates, policy gates, non-k8s targets (Helm, ECS, Lambda, SSH, Terraform).

## Positioning
Two segments, two pitches:
1. **k8s-native mid-market** (v1): "Harness without the price, with better verification."
2. **Harness enterprise base** (v2): "Switch and save. Import your pipelines, keep your governance."

Decide after launch which to chase based on who shows up. Build the importer early regardless, it's the demo.
