# Single entry point for development tasks. Run `make help` for the list.
PYTHON ?= python3
GO_MODULES := api pkg/license tools/release/offline deploy/offline services/agent services/control tests/e2e
DOCKER ?= docker
CONTROL_IMAGE ?= ownsolid/control:dev
E2E_BIN ?= bin/e2e
IMAGE_PATHS := api pkg/license deploy/images/control tools/release/offline services/control runtimes/data-processing

.PHONY: help check check-layout check-roadmap test-tools build test image-control e2e-binaries test-offline

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

# The build context is a tar of tracked files, so local build output and secrets cannot
# enter an image. DOCKER may be remote: make image-control DOCKER='ssh ownsolid-cpu docker'
image-control: ## Build the control plane image from source (CONTROL_IMAGE, DOCKER)
	@git ls-files -z --cached --others --exclude-standard -- $(IMAGE_PATHS) \
		| COPYFILE_DISABLE=1 tar --format=ustar --no-xattrs --null -T - -cf - \
		| $(DOCKER) build -t $(CONTROL_IMAGE) -f deploy/images/control/Dockerfile -

e2e-binaries: ## Build the Linux amd64 installer and end-to-end test for tests/e2e/run-single-node.sh
	@mkdir -p $(E2E_BIN)
	@cd deploy/offline && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o ../../$(E2E_BIN)/ownsolid-install ./cmd/install
	@cd tests/e2e && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o ../../$(E2E_BIN)/e2e.test .
	@cp deploy/compose/compose.yaml $(E2E_BIN)/compose.yaml

test-offline: ## Run the release factory tests that need Linux (HOST=ownsolid-cpu)
	@tools/dev/test-offline.sh $(HOST)
