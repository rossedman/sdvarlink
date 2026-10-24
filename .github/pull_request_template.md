## What and why

<!-- What does this change, and why? Link issues with "Fixes #123". -->

## How it was tested

<!-- New or changed tests, fuzz runs, benchmark comparisons (benchstat output). -->

## Checklist

- [ ] `make check` passes
- [ ] Generated code is fresh (`make generate docs-reference` shows no diff)
- [ ] New behavior has tests (with `varlinktest` where a service is involved); changes to a critical path in CONTRIBUTING.md have a benchmark or fuzz target
- [ ] Every new function has a doc comment; public API changes are documented and listed in CHANGELOG.md
- [ ] The PR title is a Conventional Commit (`feat:`, `fix:`, `idl:`, ...)
