# sdvarlink developer tasks. Run `make help` for a list.

GO        ?= go
PKGS      := ./...
SYSTEMD_VERSION ?=
FUZZTIME  ?= 30s

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
cover: ## Write a coverage report to coverage.html
	$(GO) test -coverprofile=coverage.out ./varlink/... ./idl/... ./varlinktest/... ./internal/...
	$(GO) tool cover -html=coverage.out -o coverage.html

.PHONY: integration
integration: ## Run integration tests against the host's systemd
	$(GO) test -tags integration -count=1 -v ./integration/

.PHONY: fuzz
fuzz: ## Fuzz the frame reader, codec and IDL parser for FUZZTIME each
	$(GO) test ./internal/wire/ -run '^$$' -fuzz FuzzReadMessage -fuzztime $(FUZZTIME)
	$(GO) test ./varlink/ -run '^$$' -fuzz FuzzUnmarshal -fuzztime $(FUZZTIME)
	$(GO) test ./idl/ -run '^$$' -fuzz FuzzParse -fuzztime $(FUZZTIME)

.PHONY: bench
bench: ## Run benchmarks against the fake server
	$(GO) test -run '^$$' -bench . -benchmem ./varlink/

.PHONY: lint
lint: ## Run golangci-lint and gofmt checks
	@test -z "$$(gofmt -l . | grep -v '^site/')" || (gofmt -l .; exit 1)
	$(GO) vet $(PKGS)
	golangci-lint run

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

.PHONY: docs-deps
docs-deps: ## Install MkDocs and plugins
	pip install -r docs/requirements.txt

.PHONY: docs-serve
docs-serve: docs-reference ## Serve the docs at http://127.0.0.1:8000
	mkdocs serve

.PHONY: docs
docs: docs-reference ## Build the docs site into site/
	mkdocs build --strict

.PHONY: release-check
release-check: ## Validate the GoReleaser config
	goreleaser check

.PHONY: snapshot
snapshot: ## Build a local GoReleaser snapshot into dist/
	goreleaser release --snapshot --clean
