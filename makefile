# recipes use bash for pipefail support (ubuntu's default sh is dash)
SHELL := /bin/bash

GIT_COMMIT=$(shell git describe --always --long --dirty)
# falls back to dev when there is no tag yet (the || must wrap git, not sed, or it never fires)
GIT_VERSION=$(shell (git describe --tags --dirty 2>/dev/null || echo dev) | sed 's/-\([0-9]*\)-g/+\1@g/')
TEST_TIMEOUT?=15m
empty:=
space:=$(empty) $(empty)

# dev tool binaries are built into .tools/bin (gitignored) from the versions pinned in
# .tools/go.mod (and .tools/actionlint/go.mod) - the single source of truth for make and CI;
# dependabot keeps them updated.
# overridable because binaries cannot execute from a noexec mount (e.g. an smb checkout):
# make TOOLS_BIN=~/.cache/prowlarr-mcp/bin lint
TOOLS_BIN?=.tools/bin
ACTIONLINT=$(TOOLS_BIN)/actionlint
GOFUMPT=$(TOOLS_BIN)/gofumpt
GOLANGCI_LINT=$(TOOLS_BIN)/golangci-lint

# non-Go tools also live in .tools/bin at pinned versions, but the pins are here (dependabot
# cannot bump them): shellcheck is a static haskell binary downloaded from its github release,
# yamllint is python installed into a repo-local venv. both rebuild when this makefile changes.
SHELLCHECK_VERSION=v0.11.0
YAMLLINT_VERSION=1.38.0
ZIZMOR_VERSION=v1.30.1
SHELLCHECK=$(TOOLS_BIN)/shellcheck
YAMLLINT=$(TOOLS_BIN)/yamllint
ZIZMOR=$(TOOLS_BIN)/zizmor

# golangci-lint with the azproviderlint module plugin compiled in (.tools/.custom-gcl.yml);
# lint runs use this binary, the plain go.mod one exists to bootstrap `golangci-lint custom`
GOLANGCI_LINT_MODULES=$(TOOLS_BIN)/golangci-with-modules

# one rule builds any Go tool: the import path comes from the tool directives in .tools/go.mod
# (via go list tool), so the makefile never repeats it - add a tool there and a variable above
$(TOOLS_BIN)/%: .tools/go.mod .tools/go.sum
	@echo "==> building $* (version pinned in .tools/go.mod)..."
	@mkdir -p $(TOOLS_BIN)
	@cd .tools && go build -o $(abspath $@) $$(go list tool | grep "/$*$$")

# actionlint lives in its own module (.tools/actionlint/go.mod): it pins an older go.yaml.in/yaml/v4
# release candidate than golangci-lint's dependencies and does not compile against the newer one
$(ACTIONLINT): .tools/actionlint/go.mod .tools/actionlint/go.sum
	@echo "==> building actionlint (version pinned in .tools/actionlint/go.mod)..."
	@mkdir -p $(TOOLS_BIN)
	@cd .tools/actionlint && go build -o $(abspath $@) $$(go list tool)

# explicit rules take precedence over the pattern rule above for the non-Go tools and actionlint.
# `golangci-lint custom` always writes to .tools/bin (destination in .custom-gcl.yml), so move
# the result when TOOLS_BIN points elsewhere (e.g. a local dir because the checkout is noexec)
$(GOLANGCI_LINT_MODULES): .tools/.custom-gcl.yml $(GOLANGCI_LINT)
	@echo "==> building golangci-lint with plugins (versions pinned in .tools/.custom-gcl.yml)..."
	@cd .tools && mkdir -p bin && $(abspath $(GOLANGCI_LINT)) custom
	@if [ "$(abspath $(TOOLS_BIN))" != "$(abspath .tools/bin)" ]; then mv .tools/bin/golangci-with-modules $@; fi

$(SHELLCHECK): makefile
	@echo "==> downloading shellcheck $(SHELLCHECK_VERSION)..."
	@mkdir -p $(TOOLS_BIN)
	@os=$$(uname | tr 'A-Z' 'a-z'); arch=$$(uname -m); [ "$$arch" = "arm64" ] && arch=aarch64; \
		curl -sSfL "https://github.com/koalaman/shellcheck/releases/download/$(SHELLCHECK_VERSION)/shellcheck-$(SHELLCHECK_VERSION).$$os.$$arch.tar.xz" \
		| tar -xJ -O shellcheck-$(SHELLCHECK_VERSION)/shellcheck > $@ && chmod +x $@

$(YAMLLINT): makefile
	@command -v python3 >/dev/null || (echo "python3 is required to install yamllint (macOS: xcode CLT; Debian/Ubuntu: apt install python3-venv)" && exit 1)
	@echo "==> installing yamllint $(YAMLLINT_VERSION) into $(TOOLS_BIN)/../venv..."
	@mkdir -p $(TOOLS_BIN)
	@python3 -m venv $(TOOLS_BIN)/../venv && $(TOOLS_BIN)/../venv/bin/pip install -q yamllint==$(YAMLLINT_VERSION) && ln -sf ../venv/bin/yamllint $@

$(ZIZMOR): makefile
	@echo "==> downloading zizmor $(ZIZMOR_VERSION)..."
	@mkdir -p $(TOOLS_BIN)
	@case "$$(uname)" in Darwin) target=apple-darwin;; *) target=unknown-linux-gnu;; esac; \
		arch=$$(uname -m); [ "$$arch" = "arm64" ] && arch=aarch64; \
		curl -sSfL "https://github.com/zizmorcore/zizmor/releases/download/$(ZIZMOR_VERSION)/zizmor-$$arch-$$target.tar.gz" \
		| tar -xz -O zizmor > $@ && chmod +x $@

default: fmt build

all: fmt build

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-24s\033[0m%s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Build
build: ## Compile prowlarr-mcp with version info from git
	@echo "==> building..."
	go build -ldflags "-X github.com/katbyte/go-kt/version.GitCommit=${GIT_COMMIT} -X github.com/katbyte/go-kt/version.Version=${GIT_VERSION}"

install: ## Install prowlarr-mcp into GOPATH/bin with version info from git
	@echo "==> installing..."
	go install -ldflags "-X github.com/katbyte/go-kt/version.GitCommit=${GIT_COMMIT} -X github.com/katbyte/go-kt/version.Version=${GIT_VERSION}" .

docker: ## Build the prowlarr-mcp container image with version info from git
	@echo "==> building docker image..."
	docker build --build-arg VERSION=${GIT_VERSION} --build-arg COMMIT=${GIT_COMMIT} -t prowlarr-mcp .

tools: $(ACTIONLINT) $(GOFUMPT) $(GOLANGCI_LINT) $(GOLANGCI_LINT_MODULES) $(SHELLCHECK) $(YAMLLINT) $(ZIZMOR) ## Install all pinned dev tools into .tools/bin

##@ SDK generation (internal/pandorest)
generate: pandorest-import pandorest-generate ## Import the spec into api-definitions/, then generate lib/prowlarr from it

pandorest-import: ## Import docs/prowlarr-openapi.json into api-definitions/, applying the workarounds
	@echo "==> importing the OpenAPI spec into api-definitions/..."
	go run ./internal/pandorest import

pandorest-generate: ## Generate lib/prowlarr from api-definitions/
	@echo "==> generating lib/prowlarr from api-definitions/..."
	go run ./internal/pandorest generate

pandorest-diff: ## Report what the spec in docs/ changes against the checked-in api-definitions/
	@go run ./internal/pandorest diff -quiet

##@ Formatting
fmt: $(GOFUMPT) $(GOLANGCI_LINT) ## Fix Go formatting (gofmt, gofumpt, goimports)
	@echo "==> Fixing source code with gofmt..."
	find . -name '*.go' | grep -v vendor | xargs gofmt -s -w
	@echo "==> Fixing source code with gofumpt..."
	find . -name '*.go' | grep -v vendor | xargs $(GOFUMPT) -w
	@echo "==> Fixing imports with golangci-lint (goimports)..."
	$(GOLANGCI_LINT) fmt -E goimports ./...

goimports: $(GOLANGCI_LINT) ## Fix imports with golangci-lint (goimports)
	@echo "==> Fixing imports with golangci-lint (goimports)..."
	$(GOLANGCI_LINT) fmt -E goimports ./...

##@ Linting & Dependencies
lint: $(GOLANGCI_LINT_MODULES) ## Check source code with the golangci linters (incl. azproviderlint)
	@echo "==> Checking source code against linters..."
	$(GOLANGCI_LINT_MODULES) run ./...

actionlint: $(ACTIONLINT) $(SHELLCHECK) ## Check GitHub workflows with actionlint (incl. shellcheck on run blocks)
	@echo "==> Checking workflows with actionlint..."
	@$(ACTIONLINT) -shellcheck=$(SHELLCHECK)

lint-fix: $(GOLANGCI_LINT_MODULES) ## Fix source code with all golangci linters
	@echo "==> Checking source code against linters (applying autofixes)..."
	$(GOLANGCI_LINT_MODULES) run --fix ./...

yamllint: $(YAMLLINT) ## Check YAML files with yamllint (config in .yamllint.yml)
	@echo "==> Checking YAML files with yamllint..."
	@$(YAMLLINT) -s .

shellcheck: $(SHELLCHECK) ## Check shell scripts with shellcheck
	@echo "==> Checking shell scripts with shellcheck..."
	@$(SHELLCHECK) scripts/*.sh

zizmor: $(ZIZMOR) ## Audit GitHub workflows for security issues with zizmor
	@echo "==> Auditing workflows with zizmor..."
	@$(ZIZMOR) .

gencheck: generate ## Check that the definitions and generated SDK match the spec (regenerate, then diff)
	@test -z "$$(git status --porcelain -- api-definitions lib/prowlarr)" || \
		(git status --short -- api-definitions lib/prowlarr; echo; \
		echo "api-definitions/ or lib/prowlarr is stale. Run 'make generate' and commit."; exit 1)

apicheck: ## Check that the definitions match the spec and every operation has a method in lib/prowlarr
	@echo "==> Checking API coverage of lib/prowlarr..."
	@go run ./internal/pandorest check -quiet

depscheck: ## Check that go.mod/go.sum and vendor/ are in sync
	@echo "==> Checking source code with go mod tidy..."
	@go mod tidy
	@git diff --exit-code -- go.mod go.sum || \
		(echo; echo "Unexpected difference in go.mod/go.sum files. Run 'go mod tidy' command or revert any go.mod/go.sum changes and commit."; exit 1)
	@echo "==> Checking source code with go mod vendor..."
	@go mod vendor
	@git diff --compact-summary --exit-code -- vendor || \
		(echo; echo "Unexpected difference in vendor/ directory. Run 'go mod vendor' command or revert any go.mod/go.sum/vendor changes and commit."; exit 1)
	@echo "==> Checking .tools/go.mod with go mod tidy..."
	@cd .tools && go mod tidy
	@git diff --exit-code -- .tools/go.mod .tools/go.sum || \
		(echo; echo "Unexpected difference in .tools/go.mod/go.sum. Run 'cd .tools && go mod tidy' and commit."; exit 1)
	@echo "==> Checking .tools/actionlint/go.mod with go mod tidy..."
	@cd .tools/actionlint && go mod tidy
	@git diff --exit-code -- .tools/actionlint/go.mod .tools/actionlint/go.sum || \
		(echo; echo "Unexpected difference in .tools/actionlint/go.mod/go.sum. Run 'cd .tools/actionlint && go mod tidy' and commit."; exit 1)
	@echo "==> Checking .tools/.custom-gcl.yml golangci-lint version matches .tools/go.mod..."
	@modv=$$(cd .tools && go list -m -f '{{.Version}}' github.com/golangci/golangci-lint/v2); \
		gclv=$$(grep '^version:' .tools/.custom-gcl.yml | awk '{print $$2}'); \
		[ "$$modv" = "$$gclv" ] || \
		(echo; echo "golangci-lint version mismatch: .tools/go.mod has $$modv but .tools/.custom-gcl.yml has $$gclv - update .custom-gcl.yml to match."; exit 1)

##@ Testing
test: build ## Run tests
	go test ./... -timeout ${TEST_TIMEOUT}

# each suite gets its own container, and its own ports for the fake indexers and
# applications the suite runs on the host: the SDK tests create and delete
# indexers of their own, which would trample the tool suite's fixtures, so the
# two can run side by side
PORT_acceptance=19696
INDEXER_PORT_acceptance=19691
APP_PORT_acceptance=19692
PORT_integration=19796
INDEXER_PORT_integration=19791
APP_PORT_integration=19792

test-integration: ## Run the SDK tests (lib/prowlarr) against an already-running Prowlarr
	@[ -n "${PROWLARR_SERVER}" ] && [ -n "${PROWLARR_TOKEN}" ] || \
		(echo 'PROWLARR_SERVER and PROWLARR_TOKEN must be set; or use "make testacc"'; exit 1)
	go test -tags integration -count=1 ./integration/... -timeout ${TEST_TIMEOUT} -v

test-acceptance: ## Run the tool tests (behaviour, audits) against an already-running Prowlarr
	@[ -n "${PROWLARR_SERVER}" ] && [ -n "${PROWLARR_TOKEN}" ] || \
		(echo 'PROWLARR_SERVER and PROWLARR_TOKEN must be set; or use "make testacc"'; exit 1)
	go test -tags integration -count=1 ./acceptance/... -timeout ${TEST_TIMEOUT} -v

# live SUITE [EXTRA GO TEST FLAGS] - start a container, run one suite, tear it down
define live
	@echo "==> $(1)..."
	@set -e; \
		env=".testenv-$(1).sh"; \
		export PROWLARR_TEST_CONTAINER=prowlarr-mcp-$(1) \
			PROWLARR_TEST_DATA=$${HOME}/.cache/prowlarr-mcp/testenv/$(1) \
			PROWLARR_TEST_PORT=$(PORT_$(1)) \
			PROWLARR_TEST_INDEXER_PORT=$(INDEXER_PORT_$(1)) \
			PROWLARR_TEST_APP_PORT=$(APP_PORT_$(1)); \
		scripts/testenv.sh up | grep '^export' > "$$env"; \
		trap 'st=$$?; [ $$st -eq 0 ] || scripts/testenv.sh logs; \
			scripts/testenv.sh down; rm -f '"$$env"'; exit $$st' EXIT; \
		. ./"$$env"; \
		go test -tags integration -count=1 ./$(1)/... -timeout ${TEST_TIMEOUT} -v $(2)
endef

testacc-integration: ## SDK tests in a throwaway Prowlarr
	$(call live,integration)

testacc-acceptance: ## Tool tests in a throwaway Prowlarr
	$(call live,acceptance)

testacc: testacc-integration testacc-acceptance ## Run both live suites, each in its own container

# Coverage has to span all three suites or it lies: the unit tests alone report
# a fraction for tools/, because almost everything real happens in the live
# suites behind the integration tag. Each writes binary coverage into its own
# directory and covdata merges them, which is stdlib tooling rather than a
# third-party merger.
COVERDIR?=.coverage
COVERPKG=./tools/...,./lib/...,./cli/...,./internal/...
SDKS=/lib/prowlarr/
COVERDIRS=$(COVERDIR)/unit,$(COVERDIR)/integration,$(COVERDIR)/acceptance

cover: ## Run every suite with coverage and report the total
	@rm -rf $(COVERDIR)
	@mkdir -p $(COVERDIR)/unit $(COVERDIR)/integration $(COVERDIR)/acceptance
	@echo "==> unit..."
	@go test -count=1 -coverpkg=$(COVERPKG) ./tools/ ./cli/ ./lib/... ./internal/... \
		-args -test.gocoverdir=$(CURDIR)/$(COVERDIR)/unit >/dev/null
	@$(MAKE) --no-print-directory cover-integration
	@$(MAKE) --no-print-directory cover-acceptance
	@go tool covdata textfmt -i=$(COVERDIRS) -o=$(COVERDIR)/coverage.all
	@# lib/prowlarr is generated, one mechanical method per operation (122 of
	@# them); the suites exercise the ones the tools rely on, so the total is for
	@# the hand-written code and the SDK gets a line of its own (the per-package
	@# figures below include it)
	@grep -v '$(SDKS)' $(COVERDIR)/coverage.all > $(COVERDIR)/coverage.out
	@(head -1 $(COVERDIR)/coverage.all; grep '$(SDKS)' $(COVERDIR)/coverage.all) > $(COVERDIR)/coverage-sdk.out
	@echo
	@go tool cover -func=$(COVERDIR)/coverage.out | tail -1
	@printf 'generated SDK:\t\t\t\t\t\t(statements)\t%s\n' "$$(go tool cover -func=$(COVERDIR)/coverage-sdk.out | tail -1 | awk '{print $$NF}')"
	@echo "==> per package"
	@go tool covdata percent -i=$(COVERDIRS)

cover-integration:
	$(call live,integration,-coverpkg=$(COVERPKG) -args -test.gocoverdir=$(CURDIR)/$(COVERDIR)/integration)

cover-acceptance:
	$(call live,acceptance,-coverpkg=$(COVERPKG) -args -test.gocoverdir=$(CURDIR)/$(COVERDIR)/acceptance)

cover-html: cover ## Run every suite with coverage and open the HTML report
	@go tool cover -html=$(COVERDIR)/coverage.out

testenv-up: ## Start a throwaway Prowlarr and print the environment the live suites need
	@scripts/testenv.sh up

testenv-down: ## Remove the throwaway Prowlarr
	@scripts/testenv.sh down

check-all: build test testacc lint actionlint yamllint shellcheck depscheck gencheck apicheck ## Run build + tests (incl. live) + all linters + depscheck

.PHONY: default all help fmt goimports build docker generate pandorest-import pandorest-generate pandorest-diff lint lint-fix actionlint yamllint shellcheck zizmor gencheck apicheck depscheck check-all install tools test
