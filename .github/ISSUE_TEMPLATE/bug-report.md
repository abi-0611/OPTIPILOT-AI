---
name: Bug Report
about: Report a reproducible bug or unexpected behavior in OptiPilot AI
title: "bug: "
labels: ["bug", "needs-triage"]
assignees: []
---

## Bug Description

<!-- A clear and concise description of what the bug is. -->

## Steps to Reproduce

<!-- Provide exact steps to reproduce the behavior. -->

1.
2.
3.

## Expected Behavior

<!-- What did you expect to happen? -->

## Actual Behavior

<!-- What actually happened? Include error messages, stack traces, or unexpected output. -->

```
<!-- paste logs or error output here -->
```

## Environment

| Field | Value |
|-------|-------|
| OptiPilot version | <!-- e.g. v0.3.1 → `kubectl get pod -n optipilot-system -o jsonpath='{.items[0].spec.containers[0].image}'` --> |
| Kubernetes version | <!-- `kubectl version --short` --> |
| Helm chart version | <!-- `helm list -n optipilot-system` --> |
| Cloud provider / cluster type | <!-- e.g. EKS 1.32, GKE Autopilot, kind v0.27 --> |
| OS (if running locally) | <!-- e.g. macOS 15, Ubuntu 24.04 --> |
| Go version (if building from source) | <!-- `go version` --> |

## Component

<!-- Which component is affected? -->

- [ ] Cluster Agent (controller)
- [ ] SLO Evaluator
- [ ] Optimization Solver / CEL Engine
- [ ] Actuators (HPA / Karpenter / AppTuner)
- [ ] ML Service (forecaster / spot predictor)
- [ ] Hub / Multi-cluster
- [ ] Explainability API / Dashboard
- [ ] Helm Chart / Installation
- [ ] Documentation
- [ ] Other: <!-- describe -->

## Relevant CRDs / Config

<!-- Paste relevant CRD YAML (ServiceObjective, OptimizationPolicy, TenantProfile) if applicable. Redact sensitive values. -->

```yaml

```

## Manager Logs

<!-- Paste relevant log lines from the manager. Run: `kubectl logs -n optipilot-system -l app.kubernetes.io/component=cluster-agent --tail=100` -->

```
<!-- paste logs here -->
```

## Additional Context

<!-- Any other context, workarounds you've tried, or related issues. -->
