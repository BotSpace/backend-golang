.DEFAULT_GOAL := help

APP_NAME      ?= goapp
BIN_DIR       ?= bin
PKG           ?= ./...
DB_URL        ?= postgres://postgres:postgres@localhost:5432/botspace?sslmode=disable
MIGRATIONS    ?= ./migrations

# ------------------ help ------------------
.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ------------------ dev -------------------
.PHONY: run
run: ## Run the API (no hot reload)
	go run ./cmd/api

.PHONY: run-worker
run-worker: ## Run the worker
	go run ./cmd/worker

.PHONY: gen-keys
gen-keys: ## Generate a fresh RS256 key pair into keys/ (run per environment)
	@mkdir -p keys
	@openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out keys/jwt_private.pem
	@openssl rsa -in keys/jwt_private.pem -pubout -out keys/jwt_public.pem
	@chmod 600 keys/jwt_private.pem
	@echo "Wrote keys/jwt_private.pem + keys/jwt_public.pem — NEVER reuse across environments."

.PHONY: dev
dev: ## Run with hot reload (requires `air`)
	air

.PHONY: build
build: ## Build static binary
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BIN_DIR)/$(APP_NAME) ./cmd/api

.PHONY: build-worker
build-worker: ## Build static worker binary
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BIN_DIR)/worker ./cmd/worker

.PHONY: tidy
tidy: ## go mod tidy
	go mod tidy

# ------------------ quality ---------------
.PHONY: test
test: ## Run tests with race detector
	go test -race -count=1 $(PKG)

.PHONY: test-integration
test-integration: ## Run integration tests (requires Docker)
	go test -race -count=1 -tags integration $(PKG)

.PHONY: cover
cover: ## Test coverage report
	go test -race -coverprofile=coverage.out $(PKG)
	go tool cover -html=coverage.out -o coverage.html

.PHONY: lint
lint: ## golangci-lint
	golangci-lint run

.PHONY: fmt
fmt: ## gofmt + goimports
	gofmt -w .
	@command -v goimports >/dev/null && goimports -w . || echo "goimports not installed"

# ------------------ db --------------------
.PHONY: db-up
db-up: ## Start Postgres in docker
	docker compose up -d db

.PHONY: db-down
db-down: ## Stop Postgres
	docker compose down

.PHONY: db-psql
db-psql: ## Open psql shell on local DB
	docker compose exec db psql -U postgres -d botspace

.PHONY: migrate-up
migrate-up: ## Apply all up migrations
	migrate -path $(MIGRATIONS) -database "$(DB_URL)" up

.PHONY: migrate-down
migrate-down: ## Roll back last migration
	migrate -path $(MIGRATIONS) -database "$(DB_URL)" down 1

.PHONY: migrate-new
migrate-new: ## Create new migration: make migrate-new NAME=add_widgets
	@test -n "$(NAME)" || (echo "NAME=<migration_name> required"; exit 1)
	migrate create -ext sql -dir $(MIGRATIONS) -seq $(NAME)

.PHONY: migrate-force
migrate-force: ## Force migration version: make migrate-force V=1
	@test -n "$(V)" || (echo "V=<version> required"; exit 1)
	migrate -path $(MIGRATIONS) -database "$(DB_URL)" force $(V)

# ------------------ sqlc ------------------
.PHONY: sqlc
sqlc: ## Generate sqlc code from queries/*.sql
	sqlc generate

# ------------------ swagger ---------------
.PHONY: swag
swag: ## Generate Swagger/OpenAPI docs from handler annotations
	swag init -g cmd/api/main.go --output docs --parseDependency --parseInternal

.PHONY: swag-fmt
swag-fmt: ## Format swag annotations in source files
	swag fmt -g cmd/api/main.go

# ------------------ docker ----------------
.PHONY: docker-build
docker-build: ## Build docker image
	docker build -t $(APP_NAME):dev .

.PHONY: up
up: ## Run full stack (db + api) in docker
	docker compose up --build

.PHONY: down
down: ## Tear down docker stack
	docker compose down -v

# ------------------ tools -----------------
.PHONY: install-tools
install-tools: ## Install dev tools (air, migrate, sqlc, swag, golangci-lint)
	go install github.com/air-verse/air@latest
	go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	go install github.com/swaggo/swag/cmd/swag@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
