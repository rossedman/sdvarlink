# sdvarlink developer tasks. Run `make help` for a list.
# Run `make check` before opening a pull request; CI runs the same checks.
# Shorten the fuzz runs locally with `make check FUZZTIME=5s`.

GO        ?= go
PKGS      := ./...
SYSTEMD_VERSION ?=
FUZZTIME  ?= 30s
COVERAGE  ?= 90
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
BENCHSTAT   := golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e
MKDOCS      := uv run --no-project --with-requirements docs/requirements.txt mkdocs

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: test
test: ## Run unit tests
	$(GO) test $(PKGS)

.PHONY: race
race: ## Run unit tests with the race detector
	$(GO) test -race -count=1 $(PKGS)

.PHONY: cover
cover: ## Fail if coverage of hand-written code is below COVERAGE%; writes coverage.html
	$(GO) test -coverprofile=coverage.out $(PKGS)
	@# Generated bindings (systemd/), the generator's main package, examples and
	@# integration tests (which need a real systemd) are not counted.
	@{ head -1 coverage.out; grep -v '^mode:' coverage.out | grep -v -e '/systemd/' -e '/cmd/' -e '/examples/' -e '/integration/'; } > coverage.hand.out
	$(GO) tool cover -html=coverage.hand.out -o coverage.html
	@$(GO) tool cover -func=coverage.hand.out | awk -v min=$(COVERAGE) '/^total:/ { sub("%", "", $$3); print "coverage " $$3 "% (minimum " min "%)"; if ($$3 + 0 < min) exit 1 }'

.PHONY: integration
integration: ## Run integration tests against the host's systemd
	$(GO) test -tags integration -count=1 -v ./integration/

.PHONY: fuzz
fuzz: ## Fuzz the frame reader, codec, parameter validation and IDL parser for FUZZTIME each
	$(GO) test ./internal/wire/ -run '^$$' -fuzz '^FuzzReadMessage$$' -fuzztime $(FUZZTIME)
	$(GO) test ./varlink/ -run '^$$' -fuzz '^FuzzUnmarshal$$' -fuzztime $(FUZZTIME)
	$(GO) test ./varlink/ -run '^$$' -fuzz '^FuzzValidateParams$$' -fuzztime $(FUZZTIME)
	$(GO) test ./idl/ -run '^$$' -fuzz '^FuzzParse$$' -fuzztime $(FUZZTIME)

.PHONY: bench
bench: ## Run benchmarks and save them to bench.txt (compare with `make benchstat`)
	$(GO) test -run '^$$' -bench . -benchmem -count=6 ./varlink/ ./internal/wire/ ./idl/ | tee bench.txt

.PHONY: bench-smoke
bench-smoke: ## Run every benchmark once
	$(GO) test -run '^$$' -bench . -benchtime=1x $(PKGS)

.PHONY: benchstat
benchstat: ## Compare bench.old.txt with bench.txt
	$(GO) run $(BENCHSTAT) bench.old.txt bench.txt

.PHONY: lint
lint: ## Run golangci-lint and gofmt checks
	@test -z "$$(gofmt -l . | grep -v '^site/')" || (gofmt -l .; exit 1)
	$(GO) vet $(PKGS)
	golangci-lint run

.PHONY: doccheck
doccheck: ## Fail if a function, method or package has no doc comment
	$(GO) run ./tools/doccheck ./...

.PHONY: vuln
vuln: ## Check dependencies for known vulnerabilities
	$(GO) run $(GOVULNCHECK) $(PKGS)

.PHONY: generate
generate: ## Regenerate systemd/ packages from idl/ snapshots
	$(GO) run ./cmd/sdvarlink-gen generate

.PHONY: check-generated
check-generated: generate docs-reference ## Fail if generated code or docs are stale
	@git diff --exit-code -- systemd docs/reference mkdocs.yml || \
		(echo "generated files are stale: run 'make generate docs-reference'"; exit 1)

.PHONY: idl
idl: ## Extract IDL for SYSTEMD_VERSION from systemd sources (needs git, cpp, python3)
	@test -n "$(SYSTEMD_VERSION)" || (echo "usage: make idl SYSTEMD_VERSION=261"; exit 2)
	hack/extract-idl.sh $(SYSTEMD_VERSION)

.PHONY: idl-capture
idl-capture: ## Capture IDL from the running systemd (needs root)
	sudo hack/capture-idl.sh

.PHONY: api-diff
api-diff: ## Print API changes between two releases: make api-diff FROM=257 TO=260
	$(GO) run ./cmd/sdvarlink-gen diff $(FROM) $(TO)

.PHONY: docs-reference
docs-reference: ## Regenerate the interface reference pages
	$(GO) run ./cmd/sdvarlink-gen docs

.PHONY: docs-serve
docs-serve: docs-reference ## Serve the docs at http://127.0.0.1:8000 (needs uv)
	$(MKDOCS) serve

.PHONY: docs
docs: docs-reference ## Build the docs site into site/; strict, so broken links fail (needs uv)
	$(MKDOCS) build --strict

.PHONY: release-check
release-check: ## Validate the GoReleaser config
	goreleaser check

.PHONY: snapshot
snapshot: ## Build a local GoReleaser snapshot into dist/
	goreleaser release --snapshot --clean --skip=sign

.PHONY: check
check: ## Run everything CI runs, except integration tests
	$(GO) build $(PKGS)
	$(MAKE) lint race cover doccheck vuln bench-smoke fuzz check-generated docs
