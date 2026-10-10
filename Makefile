GO_IMAGE ?= golang:1.25-alpine
GO_RUN   := docker run --rm -v $(CURDIR)/api:/src -w /src -e GOFLAGS=-mod=mod -e GOCACHE=/tmp/gocache -v taskflow-gocache:/tmp/gocache -v taskflow-gomod:/go/pkg/mod $(GO_IMAGE)

.PHONY: help build test lint fmt swag migrate-new web-install web-dev web-build up down logs

help: ## Список команд
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'

build: ## Собрать бинарник api (в контейнере golang)
	$(GO_RUN) go build -o bin/taskflow ./cmd/taskflow

test: ## Тесты сервера
	$(GO_RUN) go test ./... -count=1

lint: ## golangci-lint
	docker run --rm -v $(CURDIR)/api:/src -w /src golangci/golangci-lint:v2.6 golangci-lint run ./...

fmt: ## gofmt
	$(GO_RUN) gofmt -l -w .

swag: ## Сгенерировать Swagger 2.0 из аннотаций
	$(GO_RUN) sh -c 'go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/taskflow/main.go -o internal/docs'

migrate-new: ## Новая миграция: make migrate-new name=create_users
	@test -n "$(name)" || (echo "name= обязателен"; exit 1)
	@ts=$$(date +%Y%m%d%H%M%S); touch api/migrations/$${ts}_$(name).up.sql api/migrations/$${ts}_$(name).down.sql; ls api/migrations | tail -2

web-install: ## npm ci для фронтенда
	cd web && npm ci

web-dev: ## Dev-сервер Vite
	cd web && npm run dev

web-build: ## Сборка фронтенда в api/internal/web/dist (embed)
	cd web && npm run build

up: ## Поднять стенд
	docker compose up -d --build --wait

down: ## Остановить стенд
	docker compose down

logs: ## Логи api и worker
	docker compose logs -f api worker
