SQLC_VERSION := v1.31.1
OAPI_CODEGEN_VERSION := v2.5.1

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  %-12s %s\n", $$1, $$2}'

.PHONY: setup
setup: ## Install web dependencies and create .env
	cd web && npm install
	@test -f .env || (cp .env.example .env && echo "Created .env from .env.example")

.PHONY: up
up: ## Start Postgres and Mailpit
	docker compose up -d --wait postgres
	docker compose up -d mailpit

.PHONY: down
down: ## Stop the docker-compose services
	docker compose down

.PHONY: server
server: ## Run the API server on :8080
	cd server && go run ./cmd/cruuze

.PHONY: web
web: ## Run the web dev server on :5173
	cd web && npm run dev

.PHONY: generate
generate: generate-server generate-web ## Regenerate code from SQL queries and the OpenAPI spec

.PHONY: generate-server
generate-server:
	cd server && go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate
	cd server && go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) -config oapi-codegen.yaml ../api/openapi.yaml

.PHONY: generate-web
generate-web:
	cd web && npm run gen:api

.PHONY: test
test: ## Run all tests (server integration tests need TEST_DATABASE_URL)
	cd server && go vet ./... && go test ./...
	cd web && npm run typecheck && npm test

.PHONY: e2e
e2e: ## Run the browser tests (needs E2E_DATABASE_URL, and `npx playwright install chromium` once)
	cd web && npm run e2e

.PHONY: docker
docker: ## Build the production image as cruuze:latest
	docker build -t cruuze .

.PHONY: build
build: ## Build a single binary with the web app embedded, at server/bin/cruuze
	cd web && npm run build
	rm -rf server/internal/webui/dist && cp -R web/dist server/internal/webui/dist
	touch server/internal/webui/dist/.gitkeep
	cd server && go build -tags embedweb -o bin/cruuze ./cmd/cruuze
