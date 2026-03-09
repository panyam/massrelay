
tests: test-go test-ts

test-go:
	go test ./...

test-ts:
	cd ts && npm test
	npx vitest run

## Local dev stack (relay + Grafana in Docker)

local-up: ## Build and start relay + Grafana LGTM stack
	docker compose -f deploy/local/docker-compose.yml up -d --build
	@echo ""
	@echo "Relay:   http://localhost:8787"
	@echo "Grafana: http://localhost:3000"

local-down: ## Stop relay + Grafana LGTM stack
	docker compose -f deploy/local/docker-compose.yml down

local-logs: ## Tail relay logs
	docker compose -f deploy/local/docker-compose.yml logs -f relay

local-rebuild: ## Rebuild relay image and restart
	docker compose -f deploy/local/docker-compose.yml up -d --build relay

## Run relay natively with OTEL (no Docker for relay)

run-otel: ## Run relay natively with OTEL metrics → local Grafana stack
	OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
	OTEL_SERVICE_NAME=massrelay-local \
	OTEL_METRICS_PROMETHEUS=true \
	go run . -port 8787
