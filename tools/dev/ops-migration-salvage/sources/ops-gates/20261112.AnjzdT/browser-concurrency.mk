test-web-browser: web-install
	@cd web && pnpm --filter ./apps/console run test:browser --workers=2
	@cd web && pnpm --filter ./apps/portal run test:browser --workers=2
