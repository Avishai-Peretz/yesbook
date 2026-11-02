# Live Pipeline UI development guide

This guide uses small vertical milestones: read the contract, implement one observable behavior, test it, run focused checks, and review the diff before continuing.

## Start locally from PowerShell

Prerequisites: Go, Node.js/npm, Docker Desktop, and `golangci-lint`. Run each long-lived process in a separate PowerShell window.

Backend with durable PostgreSQL data and Docling (default Automatic+images path):

```powershell
Set-Location C:\Users\avi-p\Go_first_project\yesbook
# Preferred: make starts Postgres + Docling and sets YESBOOK_DOCLING_URL.
make dev
```

Or step by step:

```powershell
Set-Location C:\Users\avi-p\Go_first_project\yesbook
docker compose up -d --wait postgres
docker compose up -d docling
$env:YESBOOK_DATABASE_URL = 'postgres://yesbook:yesbook@localhost:5433/yesbook?sslmode=disable'
$env:YESBOOK_DOCLING_URL = 'http://localhost:5001'
go run ./cmd/yesbook-dev
```

Docling is part of the default local path (not an opt-in profile). Without `YESBOOK_DOCLING_URL`, Automatic+images records `extractor_unset`, continues to Gemini/OpenAI when configured, and otherwise ends with `chain_exhausted` plus a needs-review payload. Pipeline startup logs a warning when Docling is unset.

The development server listens on `http://localhost:8080`. Check it from another window:

```powershell
(Invoke-WebRequest http://localhost:8080/healthz).StatusCode
```

For a disposable in-memory backend, omit the database URL. Keep Docling running if you want Automatic+images figure extraction:

```powershell
Remove-Item Env:YESBOOK_DATABASE_URL -ErrorAction SilentlyContinue
docker compose up -d docling
$env:YESBOOK_DOCLING_URL = 'http://localhost:5001'
go run ./cmd/yesbook-dev
```

Frontend, after the `web` scaffold is present:

```powershell
Set-Location C:\Users\avi-p\Go_first_project\yesbook\web
npm install
npm run dev
```

Use the URL printed by Vite. The development proxy, not browser code, must attach the published local API key. Never put credentials in a `VITE_*` variable: those values are shipped to the browser. Local defaults are for development only; supply real secrets through the deployment environment.

Stop the Go and Vite processes with `Ctrl+C`, then stop local infrastructure with `docker compose down`.

## State and component boundaries

- Keep route components responsible for page composition, feature components responsible for one user behavior, and small presentational components responsible for markup.
- Keep request/response types and fetch logic in the API layer. Components should not build URLs, attach authentication, or decode wire errors.
- TanStack Query owns server state: documents, timelines, content, loading/error status, cache lifetime, and refetching.
- React state owns transient UI state: selected tab, open disclosure, draft filter text, and local form input. Do not copy query data into component state.
- Prefer explicit states such as loading, empty, error, ready, partial, and review over combinations of unrelated booleans.

## Markup, accessibility, and behavior tests

Start with semantic HTML: `main`, headings in order, real `button` and `a` elements, labelled form controls, lists for event sequences, and tables only for tabular results. Preserve visible keyboard focus, support keyboard operation, associate errors with their fields, and announce meaningful asynchronous status without repeatedly interrupting screen-reader users. Do not use color as the only status signal.

Test behavior through roles, labels, visible text, and user actions. Cover loading, empty, error, retry, unauthorized, malformed data, and success states. Avoid assertions about component internals, CSS classes, implementation-specific hook calls, or snapshots that do not prove behavior.

Every request or polling loop must accept an `AbortSignal`. Cancel it when a component unmounts, a query key changes, or a newer action replaces it. Never overlap polls; back off after transient failures and stop after a terminal state.

## Milestone workflow and exercises

1. **Foundation:** prove backend availability and API-client handling of success, `{error}`, unauthorized, cancellation, and malformed data. Exercise: add a fixture for one malformed response and write the user-visible failure assertion first.
2. **Upload:** complete register → signed PUT → submit, including validation, progress, expiry, retry, and keyboard use. Exercise: add an unsupported-file test and make focus return to the file control.
3. **Live timeline:** render persisted stages, retries, attempts, costs, progress, and failures; cancel polling on unmount and stop on final states. Exercise: add a terminal failure fixture and prove no later poll occurs.
4. **History:** implement tenant-scoped filters and cursor pagination without duplicates, with empty/error/page-transition tests. Exercise: simulate an overlapping second page and keep each document once.
5. **Results:** render every content block kind, RTL text, partial output, provenance, and review warnings. Exercise: add an RTL fixture and verify document direction and accessible table headers.
6. **Hardening:** run backend and frontend checks plus one browser happy path. Production packaging lives in [`production-web.md`](production-web.md) (`cmd/web` BFF, IAP→tenant map, GCS CORS, `Dockerfile.web`). Exercise: cancel the happy path during navigation and verify that no stale update appears.

For each milestone, record the React practice used and complete its exercise without broadening the milestone.

## Review checklist

- [ ] The change is one vertical behavior with a clear component/API boundary.
- [ ] Server state remains in TanStack Query; only transient interaction state is local.
- [ ] Loading, empty, error, retry, and success behavior is explicit.
- [ ] Semantic elements, labels, focus, keyboard use, announcements, RTL, and non-color status cues are correct.
- [ ] Tests assert user-observable behavior and important failure paths.
- [ ] Requests and polling cancel cleanly, do not overlap, and stop at terminal states.
- [ ] No secret appears in source, browser bundles, fixtures, logs, screenshots, or committed environment files.
- [ ] Errors are actionable without exposing credentials or sensitive backend details.
- [ ] Focused checks pass; before hardening, run `go build ./...`, `go vet ./...`, `go test -race ./...`, and `golangci-lint run ./...`; in `web`, run `npm run lint`, `npm run typecheck`, `npm test`, and `npm run build`.
