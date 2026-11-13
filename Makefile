.PHONY: setup db-up db-down migrate api worker test lint fmt

setup:            ## Install all dependencies
	pnpm install
	cd apps/api && go mod download
	cd apps/worker && uv sync

db-up:            ## Start local Postgres
	docker compose -f infra/docker/compose.yaml up -d --wait

db-down:
	docker compose -f infra/docker/compose.yaml down

migrate:          ## Apply DB migrations locally
	cd apps/api && go run ./cmd/swastik migrate up

api:              ## Run the Go API
	cd apps/api && go run ./cmd/swastik serve

worker:           ## Run the Python worker
	cd apps/worker && uv run fastapi dev src/swastik_worker/main.py

test:             ## Run every test suite
	cd apps/api && go test -race ./...
	cd apps/worker && uv run pytest -q
	pnpm test

lint:             ## Lint everything
	cd apps/api && golangci-lint run ./...
	cd apps/worker && uv run ruff check . && uv run ruff format --check .
	pnpm lint

fmt:
	cd apps/api && gofmt -w .
	cd apps/worker && uv run ruff format .
	pnpm lint:fix
