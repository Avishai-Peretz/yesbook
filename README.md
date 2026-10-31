# Yesbook

Yesbook is a multi-tenant PDF ingestion and extraction platform. It accepts a
PDF upload, routes each page through a configurable extractor chain, validates
the result, escalates when quality checks fail, and publishes structured
content with provenance, coverage, and review guidance.

This repository is an independently developed implementation inspired by a PDF
processing architecture exercise discussed during an interview process at
Stash.gg. It is not affiliated with Stash; it is my own attempt to turn that
system-design conversation into working software.

## What it does

1. Register a document and receive a short-lived signed upload URL.
2. Upload the PDF directly to object storage.
3. Submit the document to start asynchronous processing.
4. Follow a stage timeline: verify, cache lookup, prepare, extract, validate,
   assemble, publish.
5. Fetch quality-gated content blocks (text, tables, figures, formulas) with
   page anchors, directionality, and extractor provenance.
6. Optionally publish document lifecycle events through a transactional outbox.

Local development runs the whole system in one process. Production splits into
Cloud Run services, a Cloud Run Job worker, Cloud Workflows retries, GCS, and
Pub/Sub.

## Architecture

```mermaid
flowchart TB
  Browser[React_SPA] -->|"same origin /api"| WebBFF[cmd_web]
  Browser -->|"signed PUT"| Staging[GCS_or_localblob_staging]
  WebBFF -->|"tenant API key"| DocAPI[document_api]
  WebBFF -->|"tenant API key"| ContAPI[content_api]
  DocAPI --> Postgres[(PostgreSQL)]
  DocAPI --> Staging
  DocAPI -->|"Start job"| Workflows[Cloud_Workflows_or_local_runner]
  Workflows --> Worker[cmd_worker]
  Worker --> Postgres
  Worker --> Objects[GCS_originals_evidence_derived]
  Worker --> Extractors[PDFium_Docling_DocumentAI_Gemini_OpenAI]
  Postgres --> Outbox[cmd_outbox_publisher]
  Outbox --> PubSub[Pub_Sub_or_log_publisher]
  ContAPI --> Postgres
  ContAPI --> Objects
```

Hexagonal boundaries are intentional:

- Domain and use cases live in `internal/document`, `internal/ingest`,
  `internal/processing`, `internal/content`, and `internal/outbox`.
- Infrastructure adapters live in `internal/adapter/*`.
- Composition happens only in `cmd/*` and `internal/app`.
- Interfaces are declared by the package that consumes them; dependencies are
  passed through constructors.

## Technology stack

| Area | Choices actually used |
| --- | --- |
| Backend | Go 1.27, `net/http`, `log/slog` |
| Persistence | PostgreSQL 17, pgx, golang-migrate |
| Objects | GCS in production; disk-backed localblob for development |
| Extraction | PDFium (WASM), Docling, Document AI, Gemini, OpenAI |
| Async | Cloud Workflows + Cloud Run Job; localworkflow for development |
| Events | Transactional outbox → Pub/Sub (or log publisher locally) |
| Frontend | React 19, TypeScript, Vite, TanStack Query, Zod |
| Packaging | Docker / distroless images, Cloud Build, Compose for Postgres + Docling |

## Processing workflow

```text
register → signed upload → submit → claim job
  → verify PDF / store original
  → cache lookup by tenant + file hash + pipeline version
  → classify pages and build targets
  → extract with pin-then-escalate chains
  → validate quality checks
  → assemble ready / partial / needs_review / blocked
  → commit version + outbox event atomically
  → relay events; serve content through Content API
```

Important behaviors implemented in code:

- Optimistic concurrency on jobs via a revision field.
- Per-document attempt, duration, and cost budgets.
- Evidence stored under the job so retries resume instead of restarting from
  scratch.
- Cache keys include pipeline version so routing/policy changes invalidate
  stale results.
- Local and cloud runners retry transient failures, then attempt to block the
  job. Block is ownership-aware so a losing duplicate execution cannot cancel a
  healthy owner.

## Key engineering decisions

| Decision | Alternative considered | Why this approach |
| --- | --- | --- |
| Hexagonal packages with consumer-owned interfaces | Shared `ports` package or DI framework | Keeps domain free of GCP/Postgres and makes tests use fakes without a container |
| Document API + Content API + worker split | One production binary | Matches Cloud Run scaling and lets extractors stay out of the public API image |
| Transactional outbox | Publish directly from the request path | Status changes and events commit together; publishers retry independently |
| Pin-then-escalate extractor routing | Single OCR provider | Cheap local text extraction first; escalate only when validation asks for it |
| Signed uploads | Proxying PDF bytes through the API | Keeps large uploads off the API process and works the same locally and on GCS |
| Same-origin web BFF | Browser-held API keys | Production browsers never see tenant keys; Vite injects a published local key only in development |

## Getting started

Prerequisites: Go 1.27+, Node.js 22+, Docker, Make, and optionally
`golangci-lint`.

### Backend (all-in-one)

```bash
docker compose up -d --wait postgres
docker compose up -d docling
export YESBOOK_DATABASE_URL='postgres://yesbook:yesbook@localhost:5433/yesbook?sslmode=disable'
export YESBOOK_DOCLING_URL='http://localhost:5001'
go run ./cmd/yesbook-dev
```

Or simply:

```bash
make dev
```

The API listens on `http://localhost:8080`. Health: `GET /healthz`.

Without `YESBOOK_DATABASE_URL`, `yesbook-dev` uses an in-memory store suitable
for disposable demos. Docling remains the default Automatic+images path when
`YESBOOK_DOCLING_URL` is set.

### Frontend

```bash
npm --prefix web ci
npm --prefix web run dev
```

Open the Vite URL. The Vite proxy attaches the published local API key
(`dev-tenant:dev-key-0123456789abcdef`); do not put secrets in `VITE_*`
variables.

### Production-shaped local BFF

```bash
npm --prefix web ci
npm --prefix web run build
make web
```

Open `http://127.0.0.1:8090`. See [`docs/production-web.md`](docs/production-web.md)
for IAP/tenant mapping details.

## Testing

```bash
# Fast local gate (no race detector / no Postgres)
make check-all

# Full gate used by CI: race tests, lint, Postgres integration, frontend
make check
```

PostgreSQL integration tests require:

```bash
docker compose up -d --wait postgres
export YESBOOK_TEST_DATABASE_URL='postgres://yesbook:yesbook@localhost:5433/yesbook_test?sslmode=disable'
go test ./internal/adapter/postgres/...
```

Frontend checks:

```bash
npm --prefix web ci
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
```

## Repository structure

```text
cmd/                 document-api, content-api, worker, outbox-publisher,
                     migrate, web BFF, yesbook-dev
internal/document    domain model
internal/ingest      register / submit / delete use cases
internal/processing  extraction pipeline
internal/content     published content reads
internal/outbox      event relay
internal/adapter/*   Postgres, GCS, extractors, workflows, local substitutes
internal/httpapi     API handlers
internal/web         same-origin BFF + SPA serving
migrations/          golang-migrate schema
web/                 React operations console
deploy/              Cloud Build, Workflows, setup scripts, alerts
docs/                local and production guides
```

## Entry points

| Binary | Role |
| --- | --- |
| `cmd/yesbook-dev` | Local all-in-one API + pipeline + outbox logging |
| `cmd/document-api` | Register, upload URLs, submit, list, timeline, delete |
| `cmd/content-api` | Tenant-scoped content and signed downloads |
| `cmd/worker` | Process or block one job |
| `cmd/outbox-publisher` | LISTEN/NOTIFY + sweep to Pub/Sub |
| `cmd/migrate` | Apply embedded migrations |
| `cmd/web` | SPA + identity → tenant BFF |

## Limitations and next steps

Implemented and verified in this repository:

- Adaptive extraction with budgets, evidence resume, and cache keys.
- Tenant-scoped APIs, signed uploads/downloads, delete, list, and timeline.
- Local end-to-end path and production packaging/scripts.
- Ownership-aware job blocking and idempotent workflow start attempts.

Known gaps (intentionally not oversold):

- Extractor capability discovery is not exposed to the UI; pinned modes that
  are not configured are rejected by the API.
- Final jobs cannot be “re-submitted”; upload again with a stronger mode.
- IAP JWT cryptographic verification is pluggable but not wired by default;
  production depends on IAP-enforced ingress.
- Cloud Run service-to-service auth still uses the shared API key header model;
  locking APIs behind authenticated Cloud Run invocation needs ID-token
  forwarding.
- Object publication / post-delete blob cleanup is best-effort after the DB
  commit; durable garbage collection is future work.
- Multi-instance outbox delivery is at-least-once; consumers should dedupe by
  event ID. Strict per-job publish ordering across publishers is not guaranteed.
- The initial schema was written for this project and should be reviewed before
  any real production Cloud SQL deployment.
- Default GitHub branch rename to `main`, topics, and branch protection are
  approval-gated repository settings outside this code change.

## License

MIT. See [LICENSE](LICENSE).
