# Jobs SDK

Store-agnostic Go SDK for durable job tracking. Provides a high-level abstraction for creating, tracking, and querying jobs across services with pluggable persistence.

See `~/.claude/CLAUDE.md` for org-wide Go service standards.

## Quick reference

- **Type:** SDK / Go library (not a standalone service)
- **Persistence:** Pluggable via `JobStore` interface — bring your own backend
- **Postgres backend:** `postgres/` subpackage (SQLC + scoped migrations)
- **Core package deps:** Zero — pure Go types, no heavy imports
- **Filtering:** Tag-based (AND). Status, job type, ownership — all expressed as tags.

## Project structure

```
jobs.go                Pure Go types: Job, Step, Progress, Status, all Params
store.go               JobStore interface (11 methods — the persistence contract)
client.go              Client: tracking, validation, TrackRun/TrackStep lifecycle
errors.go              Sentinel errors (ErrNotFound, ErrAlreadyFinalized, ErrNoStore)
jobs_test.go           Unit tests with in-memory mock JobStore

postgres/              Postgres backend (implements jobs.JobStore)
  pgstore.go           NewStore(pool) → jobs.JobStore, SQLC query wrappers
  migrate.go           Scoped migrations with embedded SQL
  sqlc.yaml            SQLC code generation config
  db/migrations/       SQL migration files
  db/queries/          SQLC query definitions (jobs.sql, steps.sql)
  internal/dbgen/      Generated SQLC code (never edit)

Taskfile.yml           Task runner commands
mise.toml              Tool versions (go, sqlc, task)
go.mod                 Module dependencies
```

## Architecture

```
FileProcessor / Any Service
        │
        ▼
    Jobs SDK (this module)       ← Client: RegisterJob, RegisterStep, FinalizeJob, GetJob, ListJobs
        │
        ▼
    JobStore interface           ← Pluggable persistence (11 methods)
        │
   ┌────┴────────────────┐
   ▼                     ▼
Postgres backend      Custom backend
(postgres/)           (your own impl)
   │
   ▼
PostgreSQL            ← jobs + job_steps tables, GIN-indexed tags
```

## JobStore interface

The `JobStore` interface is the persistence contract. Implement it to use any backend:

```go
type JobStore interface {
    // Job lifecycle
    CreateJob(ctx, params) (*Job, error)
    FinalizeJob(ctx, jobID, params) error
    ReportProgress(ctx, jobID, progress) error

    // Step lifecycle
    CreateStep(ctx, jobID, params) (*Step, error)
    CompleteStep(ctx, stepID, params) error
    FailStep(ctx, stepID, errMsg) error

    // Tags
    AddTags(ctx, jobID, tags) error

    // Queries
    GetJob(ctx, jobID) (*Job, error)
    GetJobByExternalReference(ctx, externalReference) (*Job, error)
    ListJobs(ctx, filter) ([]Job, error)
    GetSteps(ctx, jobID) ([]Step, error)
}
```

## Domain model

Jobs and steps are pure Go structs:

- **Status:** `type Status string` with values: `pending`, `running`, `completed`, `failed`, `cancelled`
- **Job:** ID, ExternalReference, JobType, Status, Progress, Error, Input/Output/Metadata (json.RawMessage), Tags, CreatedAt/UpdatedAt
- **Step:** ID, JobID, Name, Sequence, Status, Error, Input/Output, StartedAt/CompletedAt
- **ListFilter:** Tags (AND-combined), Limit, Offset
- **Tags:** User-defined. Status is automatically managed as a tag. All filtering is tag-based.

## Postgres backend

The `postgres/` subpackage uses:
- **SQLC** for type-safe SQL query generation
- **migrate** (`github.com/laenen-partners/migrate`) for embedded SQL migrations with scoped tracking
- **pgxpool** for connection pooling
- **UUID** primary keys for jobs and steps
- **text[] with GIN index** for tag-based filtering (`tags @> $1::text[]`)
- **Terminal state guard in SQL:** `WHERE status NOT IN ('completed', 'failed', 'cancelled')` with `:execrows`

## Tag conventions

The SDK automatically manages status tags (`pending`, `running`, `completed`, `failed`, `cancelled`). All other tags are user-defined. Common patterns:

- `owner:<id>` — who submitted the job
- `team:<id>` — which team owns it
- `type:<job_type>` — job classification
- `input:<id>` — input entity references
- `output:<id>` — output entity references

## Safety guarantees

- **Status validation:** Only known Status values accepted (Client validates before JobStore)
- **Terminal state guard:** Cannot re-finalize a completed/failed/cancelled job (`ErrAlreadyFinalized`)
- **Finalize requires terminal status:** Only completed/failed/cancelled accepted
- **Size limits:** Input/Output max 1 MiB, Metadata max 64 KiB, Step I/O max 256 KiB
- **Audit logging:** All state transitions logged with `slog.InfoContext` including previous status

## Usage

### With Postgres backend

```go
import (
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/laenen-partners/jobs"
    jobspg "github.com/laenen-partners/jobs/postgres"
)

pool, _ := pgxpool.New(ctx, connString)
jobspg.Migrate(ctx, pool, "") // uses default "jobs" scope
client := jobs.NewClient(jobspg.NewStore(pool), jobs.WithPubSub(ps))
```

### Without tracking (noop)

```go
client := jobs.NoopClient()
```

## Code conventions

- No `init()` functions.
- Errors wrapped with `fmt.Errorf("context: %w", err)`.
- Uses `slog.InfoContext` for structured logging (propagates context).
- Core package has zero heavy dependencies — pure Go types and interfaces.
- Generated code in `postgres/internal/dbgen/` — never edit manually. Regenerate with `task generate`.
- Exported helpers (`ValidateStatus`, `RebuildTags`, `HasAllTags`, `ValidateData`) available for custom JobStore implementations.
