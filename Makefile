
tests: test-go test-ts

test-go:
	go test ./...

test-ts:
	cd ts && npm test
	npx vitest run

## Dev stack — relay + Grafana LGTM in Docker (for local development only)

dev-up: ## Build and start relay + Grafana LGTM stack
	docker compose -f deploy/dev/docker-compose.yml up -d --build
	@echo ""
	@echo "Relay:   http://localhost:8787"
	@echo "Grafana: http://localhost:3000"

dev-down: ## Stop dev stack
	docker compose -f deploy/dev/docker-compose.yml down

dev-logs: ## Tail relay logs
	docker compose -f deploy/dev/docker-compose.yml logs -f relay

dev-rebuild: ## Rebuild relay image and restart
	docker compose -f deploy/dev/docker-compose.yml up -d --build relay

## Run relay natively with OTEL → dev Grafana stack

run-otel: ## Run relay natively, ship metrics to dev Grafana
	OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
	OTEL_SERVICE_NAME=massrelay-dev \
	OTEL_METRICS_PROMETHEUS=true \
	go run . -port 8787

## Production stack locally (Caddy + relay, same as VPS but with localhost)

prod-up: ## Start production stack locally (Caddy + relay)
	RELAY_DOMAIN=localhost \
	RELAY_ADMIN_TOKEN=dev-admin-token \
	docker compose -f deploy/production/docker-compose.yml up -d --build
	@echo ""
	@echo "Relay:  https://localhost (self-signed cert)"
	@echo "Admin:  curl -k -H 'Authorization: Bearer dev-admin-token' https://localhost/admin/status"

prod-down: ## Stop local production stack
	docker compose -f deploy/production/docker-compose.yml down

prod-logs: ## Tail local production stack logs
	docker compose -f deploy/production/docker-compose.yml logs -f
