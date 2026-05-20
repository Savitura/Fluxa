.PHONY: run-api run-worker migrate migrate-down test lint build tidy

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
