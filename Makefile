# Single entry point for development tasks. Run `make help` for the list.
PYTHON ?= python3
GO_MODULES := api pkg/capabilities pkg/license pkg/operatorsocket tools/release/offline tools/dev/simulator deploy/offline services/agent services/control tests/e2e
GO_MODULES += tools/audit-verify
GO_MODULES += tools/cli
DOCKER ?= docker
CONTROL_IMAGE ?= ownsolid/control:dev
E2E_BIN ?= bin/e2e
IMAGE_PATHS := api pkg/capabilities pkg/license pkg/operatorsocket deploy/images/control tools/release/offline services/control runtimes/data-processing web

.PHONY: help check check-layout check-roadmap check-web test-tools web-install generate build test test-web-browser test-agent-cost image-control e2e-binaries test-offline

help: ## Show available targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

check: check-layout check-roadmap test-tools check-web ## Run all repository checks

check-layout: ## Verify the repository layout and language rules
	@$(PYTHON) tools/lint/layout/check_layout.py .

check-roadmap: ## Verify roadmap item IDs and the progress table
	@$(PYTHON) tools/dev/roadmap_progress.py --check

test-tools: ## Run tests for repository tooling
	@$(PYTHON) -m unittest discover -s tools/lint/layout/tests -p 'test_*.py'

web-install: ## Install the web workspace's locked dependencies; install scripts never run
	@cd web && pnpm install --frozen-lockfile --ignore-scripts

check-web: web-install ## Lint, format-check and type-check the web workspace; check generated API types
	@cd web && pnpm generate:check && pnpm lint && pnpm format:check && pnpm typecheck

generate: web-install ## Regenerate the web API types after changing api/openapi
	@cd web && pnpm generate

build: web-install ## Build every Go module, the gateway and the web applications
	@for module in $(GO_MODULES); do (cd $$module && go build ./...) || exit 1; done
	@cd services/gateway && cargo build --locked
	@cd web && pnpm build

# Temporary paths must not contain symlinks; macOS links /var to /private/var.
test: web-install test-web-browser ## Run Go, gateway and web tests
	@export TMPDIR="$$(cd "$${TMPDIR:-/tmp}" && pwd -P)/"; \
	for module in $(GO_MODULES); do (cd $$module && go test ./... -count=1) || exit 1; done
	@cd services/gateway && cargo test --locked
	@cd web && pnpm test

# Needs Chromium once per machine: cd web/apps/console && pnpm exec playwright install chromium
test-web-browser: web-install ## Run the browser tests of the console and portal
	@cd web && pnpm test:browser

# The test skips without a database, so a missing setting must fail here instead of passing.
test-agent-cost: ## Measure every agent call against its declared cost (PROJECT_ONE_TEST_DSN, PROJECT_ONE_DATA_PYTHON)
	@test -n "$$PROJECT_ONE_TEST_DSN" && test -n "$$PROJECT_ONE_DATA_PYTHON" || { echo "PROJECT_ONE_TEST_DSN and PROJECT_ONE_DATA_PYTHON are required"; exit 1; }
	@export TMPDIR="$$(cd "$${TMPDIR:-/tmp}" && pwd -P)/"; umask 077; \
	cd services/control && go test ./internal/modules/training -run '^TestAgentCallCostDoesNotGrowWithTheFleet$$' -count=1 -v

# The build context is a tar of tracked files, so local build output and secrets cannot
# enter an image. DOCKER may be remote: make image-control DOCKER='ssh ownsolid-cpu docker'
image-control: ## Build the control plane image from source (CONTROL_IMAGE, DOCKER)
	@git ls-files -z --cached --others --exclude-standard -- $(IMAGE_PATHS) \
		| COPYFILE_DISABLE=1 tar --format=ustar --no-xattrs --null -T - -cf - \
		| $(DOCKER) build -t $(CONTROL_IMAGE) -f deploy/images/control/Dockerfile -

e2e-binaries: ## Build the Linux amd64 installer, agent, agent egress guard test, egress recorder, agent simulator and end-to-end test for tests/e2e/run-single-node.sh
	@mkdir -p $(E2E_BIN)
	@cd services/agent && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o ../../$(E2E_BIN)/agent ./cmd/agent
	@cd services/agent && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -trimpath -tags 'docker_integration egress_integration' -o ../../$(E2E_BIN)/inference-egress.test ./internal/inference
	@cd deploy/offline && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o ../../$(E2E_BIN)/ownsolid-install ./cmd/install
	@cd tests/e2e && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o ../../$(E2E_BIN)/egress-recorder ./cmd/egress-recorder
	@cd tools/dev/simulator && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o ../../../$(E2E_BIN)/simulator .
	@cd tests/e2e && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o ../../$(E2E_BIN)/e2e.test .
	@cp deploy/compose/compose.yaml $(E2E_BIN)/compose.yaml

test-offline: ## Run the release factory tests that need Linux (HOST=ownsolid-cpu)
	@tools/dev/test-offline.sh $(HOST)
