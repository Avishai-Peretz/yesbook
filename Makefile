TEST_DATABASE_URL ?= postgres://yesbook:yesbook@localhost:5433/yesbook_test?sslmode=disable
DATABASE_URL ?= postgres://yesbook:yesbook@localhost:5433/yesbook?sslmode=disable
DOCLING_URL ?= http://localhost:5001
WEB_DIR := web
WEB_PORT ?= 8090
WEB_DOCUMENT_API_URL ?= http://127.0.0.1:8080
WEB_CONTENT_API_URL ?= http://127.0.0.1:8080
WEB_API_KEYS ?= dev-tenant:dev-key-0123456789abcdef
WEB_USER_TENANTS ?= dev@localhost:dev-tenant
WEB_AUTH_MODE ?= dev
WEB_DEV_USER ?= dev@localhost
WEB_ROOT ?= web/dist

.PHONY: build vet test test-unit test-race test-db lint bench check \
	web-lint web-typecheck web-test web-build web-check check-frontend check-all \
	docker-build docker-build-web shellcheck db-up docling-up migrate dev web

build:
	go build ./...

vet:
	go vet ./...

test-unit:
	go test -count=1 ./...

test-race:
	go test -race -shuffle=on -count=1 ./...

test: test-race

test-db: db-up
	YESBOOK_TEST_DATABASE_URL=$(TEST_DATABASE_URL) go test -race -shuffle=on -count=1 ./internal/adapter/postgres/...

lint:
	golangci-lint run ./...

bench:
	go test -bench=. -benchmem -run=^$$ ./internal/processing/...

web-lint:
	npm --prefix $(WEB_DIR) run lint

web-typecheck:
	npm --prefix $(WEB_DIR) run typecheck

web-test:
	npm --prefix $(WEB_DIR) test

web-build:
	npm --prefix $(WEB_DIR) run build

web-check: web-lint web-typecheck web-test web-build

check-frontend: web-check

# Portable local gate: build/vet/unit tests + frontend. Prefer this on Windows
# hosts without a C toolchain for -race.
check-all: build vet test-unit web-check

# Complete gate matching CI: race tests, lint, Postgres integration, frontend.
check: build vet test-race lint test-db web-check

docker-build:
	docker build --build-arg CMD=document-api -t yesbook-document-api .
	docker build --build-arg CMD=content-api -t yesbook-content-api .
	docker build --build-arg CMD=outbox-publisher -t yesbook-outbox-publisher .
	docker build --build-arg CMD=worker -t yesbook-worker .
	docker build --build-arg CMD=migrate -t yesbook-migrate .

# Frontend + BFF image; does not replace docker-build for backend binaries.
docker-build-web:
	docker build -f Dockerfile.web -t yesbook-web .

shellcheck:
	shellcheck deploy/setup.sh deploy/db-vm-startup.sh

db-up:
	docker compose up -d --wait postgres

docling-up:
	docker compose up -d docling

migrate: db-up
	YESBOOK_DATABASE_URL=$(DATABASE_URL) go run ./cmd/migrate

# Local full stack: Postgres + Docling + yesbook-dev with Docling wired in.
dev: migrate docling-up
	YESBOOK_DATABASE_URL=$(DATABASE_URL) YESBOOK_DOCLING_URL=$(DOCLING_URL) go run ./cmd/yesbook-dev

# Same-origin BFF against a local yesbook-dev (or split APIs). Requires web/dist.
web: web-build
	YESBOOK_DOCUMENT_API_URL=$(WEB_DOCUMENT_API_URL) \
	YESBOOK_CONTENT_API_URL=$(WEB_CONTENT_API_URL) \
	YESBOOK_API_KEYS=$(WEB_API_KEYS) \
	YESBOOK_USER_TENANTS=$(WEB_USER_TENANTS) \
	YESBOOK_AUTH_MODE=$(WEB_AUTH_MODE) \
	YESBOOK_DEV_USER=$(WEB_DEV_USER) \
	YESBOOK_WEB_ROOT=$(WEB_ROOT) \
	PORT=$(WEB_PORT) \
	go run ./cmd/web
