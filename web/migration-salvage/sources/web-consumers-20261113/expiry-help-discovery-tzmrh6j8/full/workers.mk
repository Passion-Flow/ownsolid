test-web-browser: web-install
	@cd web/apps/console && PLAYWRIGHT_JSON_OUTPUT_FILE="$(WEB_GATE_DIR)/console-report.json" pnpm exec playwright test --workers=4 --output "$(WEB_GATE_DIR)/console-results" --reporter=line,json
	@cd web/apps/portal && PLAYWRIGHT_JSON_OUTPUT_FILE="$(WEB_GATE_DIR)/portal-report.json" pnpm exec playwright test --workers=1 --output "$(WEB_GATE_DIR)/portal-results" --reporter=line,json
