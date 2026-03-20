# ADR-001: Composable Connect-RPC and UI Layers

**Status:** Accepted
**Date:** 2026-03-20

## Context

The jobs SDK is a pure Go library providing job tracking with pluggable persistence via the `JobStore` interface. The `postgres/` subpackage demonstrates the pattern: opt-in, separate Go module, depends on core — never the reverse.

Multiple applications need to:

1. **Expose job tracking over RPC** — services that don't embed the Go SDK directly need an API to create, query, and manage jobs.
2. **Render job status in their UI** — applications using DSX (server-side rendered with DataStar) duplicate the same job list tables, status badges, progress bars, and step timelines.
3. **Scope access based on caller identity** — an admin sees all tenant jobs; a regular user sees only their own.

## Decision

Add two new opt-in subpackages to the jobs module, following the same layering pattern as `postgres/`:

```
jobs/
  jobs.go              core types + interfaces (zero deps)
  client.go            Client orchestration
  postgres/            JobStore implementation (pgx, sqlc)
  connectrpc/          Connect-RPC handlers (new)
  ui/                  DSX UI fragments (new)
```

Each layer only depends downward. No layer is required.

### Layer architecture

```
┌─────────────┐
│   ui/       │  SSR fragments (templ + dsx)
├─────────────┤
│ connectrpc/ │  RPC handlers (proto + connect-go)
├─────────────┤
│ postgres/   │  Persistence (pgx + sqlc)
├─────────────┤
│  core       │  Pure Go types + interfaces + Client
└─────────────┘
```

An application picks the layers it needs:

| Use case | Layers |
|---|---|
| Microservice with UI | core + postgres + connectrpc + ui |
| Backend API service | core + postgres + connectrpc |
| Internal pipeline worker | core + postgres |
| Tests / prototyping | core only (in-memory mock) |

### `connectrpc/` subpackage

- Defines protobuf service in `proto/jobs/v1/jobs.proto`
- Generated Connect-RPC handlers wrap `jobs.Client`
- Thin translation layer: validate request, call Client, marshal response
- Separate `go.mod` — pulls in connect-go, protobuf; core stays dependency-free
- The handler constructor accepts a `jobs.Client`, not a `JobStore` — it benefits from Client validation and retry logic

```go
handler := jobsrpc.NewHandler(client)
mux.Handle(handler.Path(), handler)
```

### `ui/` subpackage

- Templ components using `github.com/laenen-partners/dsx` (badge, table, progress, steps)
- Fragment handlers that return DataStar SSE patches
- Works against `jobs.Job`, `jobs.Step`, `jobs.Progress` directly — no protobuf dependency
- Separate `go.mod` — pulls in dsx, templ, datastar
- The consuming app mounts handlers on its own router and wraps with its own middleware

```go
jobsUI := jobsui.NewHandlers(client, policyFn)
r.Get("/fragments/jobs", jobsUI.JobList())
r.Get("/fragments/jobs/{id}", jobsUI.JobDetail())
```

### Identity and access control

Caller identity is provided by `github.com/laenen-partners/identity` (v0.1.0), a shared zero-dependency module carrying tenant-scoped context:

```go
type Context struct {
    tenantID      string
    workspaceID   string
    principalID   string
    principalType PrincipalType // "user" | "service"
    roles         []string
}
```

Identity is propagated via `context.Context` by upstream middleware. The jobs module never defines or validates identity — it only reads it.

**Authorization is configuration, not code.** The UI and RPC handlers accept an `AccessPolicy` function at construction time. The consuming application defines what roles map to what query scopes:

```go
type AccessPolicy func(identity.Context) AccessScope

type AccessScope struct {
    ListTags  []string // injected into every ListJobs query
    CanCancel bool     // allowed to cancel jobs
}
```

Example wiring in the consuming app:

```go
jobsUI := jobsui.NewHandlers(client, func(id identity.Context) jobsui.AccessScope {
    if id.HasRole("admin") {
        return jobsui.AccessScope{
            ListTags:  []string{"tenant:" + id.TenantID()},
            CanCancel: true,
        }
    }
    return jobsui.AccessScope{
        ListTags: []string{"tenant:" + id.TenantID(), "owner:" + id.PrincipalID()},
    }
})
```

This keeps the jobs module free of hardcoded role names or permission logic. Different applications can define entirely different policies using the same module.

### UI fragments provided

| Fragment | Route pattern | Description |
|---|---|---|
| `JobList` | `GET /fragments/jobs` | Table of jobs with status badges, progress bars, clickable rows |
| `JobDetail` | `GET /fragments/jobs/{id}` | Job detail panel: metadata grid, progress, error, step timeline |

Components (reusable templ building blocks, no handlers):

| Component | Description |
|---|---|
| `StatusBadge` | Badge with variant mapped to job status |
| `ProgressBar` | Progress bar with current/total and message |
| `StepTimeline` | Vertical step visualization with status variants |
| `JobTable` | Table component for job lists |

### What fragments do NOT provide

- **HTTP routes** — the consuming app owns routing and mounts fragment handlers
- **Authentication/middleware** — the app wraps handlers in its own auth chain
- **Full pages/layouts** — fragments are building blocks; the app composes them within its DSX layout
- **Stream/SSE endpoints** — the app configures its own DataStar stream broker

## Consequences

### Positive

- **Eliminates duplication** — butler-web (and future apps) stop reimplementing job UI against protobuf types
- **Consistent UX** — all apps render jobs identically using DSX components
- **Flexible access control** — policy function pattern lets each app define its own authorization rules without modifying the module
- **Independent deployment** — each subpackage has its own `go.mod`, so dependency changes in `ui/` don't affect consumers of core or `postgres/`
- **Establishes a pattern** — other composable modules (files, documents) can follow the same core/postgres/connectrpc/ui layering

### Negative

- **More Go modules to maintain** — each subpackage is a separate module with its own version, go.sum, and release cycle
- **DSX coupling in `ui/`** — fragments are tied to the DSX component library; apps using a different UI framework cannot reuse them (but can still use core + connectrpc)
- **Templ code generation** — the `ui/` subpackage requires `templ generate` in its build pipeline

### Risks

- **DSX breaking changes** — a major DSX version bump requires updating the `ui/` subpackage. Mitigated by pinning DSX version in `go.mod` and testing against butler-web as the primary consumer.
- **Over-abstraction of policy** — if authorization needs grow complex (field-level permissions, dynamic scopes), the `AccessPolicy` function may need to evolve. Start simple; extend only when concrete requirements emerge.
