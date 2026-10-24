---
paths:
  - "**/*.sh"
  - "**/*.bash"
---

# Shell standards (enforced by Guardrails)

Guardrails formats every script you edit with shfmt and blocks the end of
your turn until touched scripts are shellcheck-clean. Write them clean the
first time.

## Safety

- Start bash scripts with `set -euo pipefail` (POSIX `sh`: `set -eu`).
- Quote every expansion: `"$var"`, `"$@"`, `"$(cmd)"`. Unquoted expansions
  word-split and glob (SC2086).
- Use `[[ ... ]]` in bash; in POSIX `sh`, use `[ ... ]` with quoted operands.
- Use `$(...)`, never backticks.
- Never parse `ls`. Loop over globs (`for f in ./*.txt`) or use
  `find ... -print0 | xargs -0`.
- Read lines with `while IFS= read -r line; do ...; done < file`.
- Use `mktemp` for temp files and clean up with `trap 'rm -rf "$tmp"' EXIT`.
- Prefix `cd` with a failure path: `cd "$dir" || exit 1`.

## Style

- Name variables in `lower_snake_case`; reserve `UPPER_CASE` for exported
  environment variables.
- Declare function locals with `local`.
- Send errors to stderr: `echo "error: ..." >&2`, and exit non-zero.
- Keep scripts small. When logic grows past ~100 lines, write it in the
  repo's main language instead.

## Never

- `# shellcheck disable=...` to get green: fix the issue. The suppression
  guard denies new disable comments.
- `eval` on anything derived from input.
