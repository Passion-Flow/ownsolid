# Single entry point for development tasks. Run `make help` for the list.
PYTHON ?= python3
GO_MODULES := api pkg/license tools/release/offline deploy/offline services/agent services/control

.PHONY: help check check-layout check-roadmap test-tools build test

help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

check: check-layout check-roadmap test-tools ## Run all repository checks

check-layout: ## Verify the repository layout and language rules
	@$(PYTHON) tools/lint/layout/check_layout.py .

check-roadmap: ## Verify roadmap item IDs and the progress table
	@$(PYTHON) tools/dev/roadmap_progress.py --check

test-tools: ## Run tests for repository tooling
	@$(PYTHON) -m unittest discover -s tools/lint/layout/tests -p 'test_*.py'

build: ## Build every Go module and the gateway
	@for module in $(GO_MODULES); do (cd $$module && go build ./...) || exit 1; done
	@cd services/gateway && cargo build --locked

# Temporary paths must not contain symlinks; macOS links /var to /private/var.
test: ## Run Go and gateway unit tests
	@export TMPDIR="$$(cd "$${TMPDIR:-/tmp}" && pwd -P)/"; \
	for module in $(GO_MODULES); do (cd $$module && go test ./... -count=1) || exit 1; done
	@cd services/gateway && cargo test --locked
