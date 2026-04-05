## Summary

<!-- One-sentence description of what this PR does and why. -->

## Type of Change

<!-- Check all that apply. -->

- [ ] Bug fix (non-breaking change that fixes an issue)
- [ ] New feature (non-breaking change that adds functionality)
- [ ] Breaking change (fix or feature that changes existing behavior)
- [ ] Documentation update
- [ ] Refactoring / technical debt (no functional change)
- [ ] CI / build / tooling change

## Motivation & Context

<!-- Why is this change needed? Link to the issue this PR resolves. -->

Fixes #<!-- issue number -->

## Changes Made

<!-- List the key files/components changed and what was done. -->

-
-
-

## Testing

<!-- Describe how you tested this change. -->

- [ ] Unit tests added / updated (run `go test ./...`)
- [ ] Helm structural tests pass (`go test ./test/helm/...`)
- [ ] E2E tests pass (`go test -tags e2e ./test/e2e/...`) — _or_ explain why not applicable
- [ ] Manual verification steps (describe below if needed)

```shell
# Commands used to test this change:

```

## Checklist

- [ ] My code follows the project's coding conventions and style (run `golangci-lint run`)
- [ ] I have updated `go.mod` / `go.sum` if dependencies changed (`go mod tidy`)
- [ ] I have run `make generate` and `make manifests` if API types changed
- [ ] I have added/updated tests that cover my changes
- [ ] All new and existing tests pass locally
- [ ] I have updated relevant documentation (`docs/`) if needed
- [ ] My PR title follows the Conventional Commits format: `type(scope): description`
  - **type**: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `ci`, `perf`
  - **example**: `feat(slo): add multi-window burn rate alerts`

## Screenshots / Logs

<!-- If applicable, add screenshots, terminal output, or log snippets. -->

## Additional Notes

<!-- Any context reviewers should know. Breaking changes? Migration steps? -->
