TEST_DATABASE_URL ?= postgres://yesbook:yesbook@localhost:5433/yesbook_test?sslmode=disable
DATABASE_URL ?= postgres://yesbook:yesbook@localhost:5433/yesbook?sslmode=disable

.PHONY: build test test-db lint db-up migrate dev

build:
	go build ./...

test:
	go test -race ./...

test-db: db-up
	YESBOOK_TEST_DATABASE_URL=$(TEST_DATABASE_URL) go test ./internal/adapter/postgres/...

lint:
	golangci-lint run ./...

db-up:
	docker compose up -d --wait postgres

migrate: db-up
	YESBOOK_DATABASE_URL=$(DATABASE_URL) go run ./cmd/migrate

dev: migrate
	YESBOOK_DATABASE_URL=$(DATABASE_URL) go run ./cmd/yesbook-dev
