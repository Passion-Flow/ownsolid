test-web-browser: web-install
	@cd web && pnpm --filter ./apps/console run test:browser --workers=4
	@cd web && pnpm --filter ./apps/portal run test:browser --workers=4
