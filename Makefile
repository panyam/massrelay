
tests: test-go test-ts

test-go:
	go test ./...

test-ts:
	cd ts && npm test
	npx vitest run

## Local observability

otel-up: ## Start local Grafana LGTM stack (metrics, logs, traces)
	docker compose -f deploy/local/docker-compose.yml up -d
	@echo "Grafana: http://localhost:3000"
	@echo "OTLP:    http://localhost:4318"

otel-down: ## Stop local Grafana LGTM stack
	docker compose -f deploy/local/docker-compose.yml down

run-otel: ## Run relay with OTEL metrics → local Grafana stack
	OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
	OTEL_SERVICE_NAME=massrelay-local \
	OTEL_METRICS_PROMETHEUS=true \
	go run . -port 8787
