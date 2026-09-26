.PHONY: run-api run-worker migrate migrate-down test lint build tidy deploy-primary deploy-secondary failover

# Run the API server
run-api:
	go run ./cmd/api

# Run the background worker
run-worker:
	go run ./cmd/worker

# Apply all pending migrations
migrate:
	go run ./cmd/api -migrate-only

# Roll back the last migration
migrate-down:
	@if [ -z "$$DATABASE_URL" ]; then \
		echo "DATABASE_URL is not set"; exit 1; \
	fi
	migrate -path db/migrations -database "$$DATABASE_URL" down 1

# Run all tests with race detector
test:
	go test ./... -race -count=1 -timeout 60s

# Run tests with coverage
test-cover:
	go test ./... -race -count=1 -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

# Lint
lint:
	golangci-lint run ./...

# Build both binaries
build:
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker

# Tidy dependencies
tidy:
	go mod tidy

# Generate sqlc (if using sqlc for query generation)
generate:
	sqlc generate

# Multi-region deployment helpers. Override COMPOSE, PRIMARY_ENV, SECONDARY_ENV,
# PROMOTE_REPLICA_CMD, and UPDATE_DNS_CMD in the deployment environment.
deploy-primary:
	@echo "Deploying API and worker in the primary region"
	$(COMPOSE) --env-file $(PRIMARY_ENV) up -d --build api worker

deploy-secondary:
	@echo "Deploying API-only secondary region"
	$(COMPOSE) --env-file $(SECONDARY_ENV) up -d --build api

failover:
	@test -n "$(PROMOTE_REPLICA_CMD)" || (echo "PROMOTE_REPLICA_CMD is required"; exit 1)
	@test -n "$(UPDATE_DNS_CMD)" || (echo "UPDATE_DNS_CMD is required"; exit 1)
	@echo "Promoting the secondary database"
	$(PROMOTE_REPLICA_CMD)
	@echo "Starting the secondary worker"
	WORKER_ENABLED=true $(COMPOSE) --env-file $(SECONDARY_ENV) up -d --build worker
	@echo "Updating DNS"
	$(UPDATE_DNS_CMD)
	@echo "Verify /health, /health/ready, and /health/live before restoring traffic"

COMPOSE ?= docker compose
PRIMARY_ENV ?= .env.primary
SECONDARY_ENV ?= .env.secondary

# Docker helpers
docker-build:
	docker compose build

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down -v

docker-logs:
	docker compose logs -f api worker

# CI locally (mimics GitHub Actions)
ci: lint test
	cd apps/web && npm ci && npm run lint && npm run build
	cd sdk && npm install && npm run typecheck && npm run build
