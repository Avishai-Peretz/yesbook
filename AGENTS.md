# AGENTS.md

## Go development

Before any Go coding, review, debugging, troubleshooting, or setup task, load the `samber/cc-skills-golang@golang-how-to` skill first — it routes to whichever other Go skills the task needs.

## Yesbook

The Go module `github.com/avi-p/yesbook` lives in `yesbook/`.

- Lightweight hexagonal layout: business rules in `internal/document`, `internal/ingest`, `internal/processing`, `internal/outbox` and `internal/content` must not import GCP SDKs or PostgreSQL. Adapters live in `internal/adapter/*`; wiring happens only in `cmd/*` and `internal/app`.
- Interfaces are declared by the package that consumes them. Dependencies are passed through constructors; no DI framework or globals.
- Errors are either logged or returned, never both. Wrap with `%w` and lowercase messages.
- Schema changes go through new `golang-migrate` files in `yesbook/migrations`; never edit an applied migration.
- Checks: `go build ./...`, `go vet ./...`, `go test -race ./...`, `golangci-lint run ./...`. PostgreSQL tests need `docker compose up -d postgres` and `YESBOOK_TEST_DATABASE_URL` pointing at the `yesbook_test` database.
