# Single entry point for development tasks. Run `make help` for the list.
PYTHON ?= python3

.PHONY: help check check-layout test-tools

help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

check: check-layout test-tools ## Run all repository checks

check-layout: ## Verify the repository layout and language rules
	@$(PYTHON) tools/lint/layout/check_layout.py .

test-tools: ## Run tests for repository tooling
	@$(PYTHON) -m unittest discover -s tools/lint/layout/tests -p 'test_*.py'
