---
paths:
  - "**/*.yml"
  - "**/*.yaml"
  - ".github/workflows/*"
---

# YAML standards (enforced by Guardrails)

Guardrails runs yamllint on every YAML file you edit, actionlint on GitHub
Actions workflows, and kubeconform (strict) on Kubernetes manifests. Errors
block the end of your turn.

## All YAML

- No duplicate keys; YAML silently keeps the last one, which changes
  meaning.
- Quote ambiguous scalars: `"yes"`, `"no"`, `"on"`, `"off"`, `"1.10"`,
  `"0755"`, and anything that looks like a date or number but is a string.
- Two-space indentation; lists indented consistently within a file.
- Respect the repo's `.yamllint` config; don't add `# yamllint disable`
  comments.

## GitHub Actions workflows

- Pin third-party actions to a full commit SHA with a version comment:
  `uses: owner/action@<sha> # v4.2.0`. First-party `actions/*` may use a
  major tag if the repo already does.
- Set least-privilege `permissions:` at the workflow or job level; default
  to `contents: read` and add only what a job needs.
- Never interpolate untrusted input (`github.event.*.title`, `body`,
  `head_ref`) directly into `run:`; pass it through `env:` and quote it.
- Set `concurrency:` for deploys and long jobs; set `timeout-minutes:`.
- Keep shell in `run:` steps shellcheck-clean; actionlint runs it.

## Kubernetes manifests

- Every container sets `resources.requests` and `resources.limits`.
- Use the standard labels: `app.kubernetes.io/name`, `instance`,
  `version`, `component`, `part-of`, `managed-by`.
- Pin images by digest or an immutable tag; never `latest`.
- Run as non-root: `securityContext.runAsNonRoot: true`,
  `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true` where
  possible, and drop all capabilities.
- Define `readinessProbe` and `livenessProbe` for long-running workloads.
- No unknown fields: kubeconform runs in strict mode against the target
  Kubernetes version.
