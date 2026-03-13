# Jobs SDK

Store-agnostic Go SDK for durable job tracking. Provides a high-level abstraction for creating, tracking, and querying long-running jobs across services with pluggable persistence.

## Install

```sh
go get github.com/laenen-partners/jobs
```

## Quick start

```go
import (
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/laenen-partners/jobs"
    jobspg "github.com/laenen-partners/jobs/postgres"
)

pool, _ := pgxpool.New(ctx, connString)

// Run migrations (optional — uses "jobs" scope by default).
jobspg.Migrate(ctx, pool, "")

// Create client with Postgres backend.
client := jobs.NewClient(jobspg.NewStore(pool))

// Or without tracking.
client := jobs.NoopClient()
```

## Jobs

A **job** represents a unit of work with a full lifecycle: creation, progress tracking, step execution, and completion or failure.

### Publishing a job

```go
job, err := client.Publish(ctx, jobs.PublishParams{
    ExternalReference: "wf-123",
    JobType:           "file_processing",
    Tags:              []string{"owner:user-456", "input:doc-789", "priority:high"},
    Input:             inputJSON,    // optional, max 1 MiB
    Metadata:          metaJSON,     // optional, max 64 KiB
})
```

The job starts with status `pending`. Tags are user-defined — use them for ownership, classification, filtering, or anything else.

### Progress

```go
err = client.ReportProgress(ctx, job.ID, jobs.Progress{
    Step:    "converting",
    Current: 3,
    Total:   10,
    Message: "Converting page 3 of 10",
})
```

The first progress report transitions the job from `pending` to `running`.

### Steps

Steps are tracked units of work within a job, each with their own status and timing.

```go
step, err := client.StartStep(ctx, job.ID, jobs.StartStepParams{
    Name: "convert", Sequence: 0,
})
// ... do work ...
err = client.CompleteStep(ctx, step.ID, jobs.CompleteStepParams{
    Output: outputJSON,
})
// or on failure:
err = client.FailStep(ctx, step.ID, "unsupported format")
```

### Adding tags during execution

```go
err = client.AddTags(ctx, job.ID, []string{"output:doc-out-1"})
```

### Finalizing

```go
err = client.Finalize(ctx, job.ID, jobs.FinalizeParams{
    Status: jobs.StatusCompleted,
    Output: outputJSON,
    Tags:   []string{"output:doc-out-1"},  // additional tags added on finalize
})
```

Terminal states (`completed`, `failed`, `cancelled`) cannot be changed. Returns `ErrAlreadyFinalized` on re-finalize.

### Orchestrated lifecycle (Run/Step)

`Run` and `Step` handle the full lifecycle automatically with best-effort retries:

```go
err := client.Run(ctx, jobs.RunParams{
    ExternalReference: "wf-123",
    JobType:           "file_processing",
    Tags:              []string{"owner:user-123", "input:doc-1"},
}, func(ctx context.Context, rc *jobs.RunContext) error {
    for i, id := range inputIDs {
        if err := client.Step(ctx, rc, "process:"+id, func(ctx context.Context) error {
            return processFile(ctx, id)
        }, jobs.WithSequence(i)); err != nil {
            return err
        }
        rc.Progress(ctx, jobs.Progress{
            Step: "processing", Current: i + 1, Total: len(inputIDs),
        })
    }
    return nil
})
```

### With DBOS workflows

Use the direct methods (`Publish`, `Finalize`, `StartStep`, etc.) inside DBOS workflows. The job lifecycle is visible and explicit — DBOS handles workflow durability, the Jobs SDK handles job tracking:

```go
func (p *Processor) ProcessFiles(ctx dbos.DBOSContext, input ProcessInput) error {
    workflowID, _ := dbos.GetWorkflowID(ctx)

    // Publish the job as a durable step.
    job, err := dbos.RunAsStep(ctx, func(sctx context.Context) (*jobs.Job, error) {
        return p.jobs.Publish(sctx, jobs.PublishParams{
            ExternalReference: workflowID,
            JobType:           "file_processing",
            Tags:              []string{"owner:" + input.OwnerID},
        })
    }, dbos.WithStepName("publish_job"))
    if err != nil {
        return fmt.Errorf("publish job: %w", err)
    }

    // Expose the job ID for external polling.
    _ = dbos.SetEvent(ctx, "job_entity_id", job.ID)

    // Ensure the job is finalized on all exit paths.
    var (
        finalStatus = jobs.StatusCompleted
        finalErr    string
    )
    defer func() {
        _, _ = dbos.RunAsStep(ctx, func(sctx context.Context) (any, error) {
            return nil, p.jobs.Finalize(sctx, job.ID, jobs.FinalizeParams{
                Status: finalStatus,
                Error:  finalErr,
            })
        }, dbos.WithStepName("finalize_job"))
    }()

    // Process each input as a tracked step.
    for i, inputID := range input.InputIDs {
        step, _ := dbos.RunAsStep(ctx, func(sctx context.Context) (*jobs.Step, error) {
            return p.jobs.StartStep(sctx, job.ID, jobs.StartStepParams{
                Name: "process", Sequence: i,
            })
        }, dbos.WithStepName(fmt.Sprintf("start_step_%d", i)))

        _, err := dbos.RunAsStep(ctx, func(sctx context.Context) (string, error) {
            return p.processFile(sctx, inputID)
        }, dbos.WithStepName(fmt.Sprintf("process_%d", i)))

        if err != nil {
            p.jobs.FailStep(ctx, step.ID, err.Error())
            finalStatus = jobs.StatusFailed
            finalErr = err.Error()
            return err
        }
        p.jobs.CompleteStep(ctx, step.ID, jobs.CompleteStepParams{})

        p.jobs.ReportProgress(ctx, job.ID, jobs.Progress{
            Step: "processing", Current: i + 1, Total: len(input.InputIDs),
        })
    }
    return nil
}
```

Every job operation is a visible call — no hidden lifecycle. DBOS `RunAsStep` checkpoints the work for replay on recovery. The defer ensures finalize runs on both success and failure paths.

### Querying

```go
job, err := client.Get(ctx, jobID)
job, err := client.GetByExternalReference(ctx, "wf-123")

// All completed jobs for a user (AND filter).
results, err := client.List(ctx, jobs.ListFilter{
    Tags:  []string{"completed", "owner:user-123"},
    Limit: 25,
})
```

### Tags

Tags are the primary filtering mechanism. The SDK automatically manages **status tags** (`pending`, `running`, `completed`, `failed`, `cancelled`). All other tags are user-defined.

Common patterns:

| Tag | Purpose |
|---|---|
| `owner:<id>` | Who submitted the job |
| `team:<id>` | Which team owns it |
| `type:<job_type>` | Job classification |
| `input:<id>` | Input entity references |
| `output:<id>` | Output entity references |
| `priority:high` | Custom filtering |

### Key identifiers

| Field | Purpose |
|---|---|
| **Job ID** | UUID assigned on creation. Used for all operations. |
| **ExternalReference** | Caller-provided idempotency key (e.g. DBOS workflow ID). Globally unique. |
| **JobType** | Classification string stored in job data. |

### Status lifecycle

```
       Publish
          |
          v
      [pending]
          |  ReportProgress
          v
      [running]
        / | \
       v  v  v
[completed] [failed] [cancelled]
```

### Errors

| Error | When |
|---|---|
| `ErrNotFound` | Job does not exist |
| `ErrAlreadyFinalized` | Job already in terminal state |
| `ErrNoStore` | Client has no backing store (NoopClient) |

## JobStore

The `JobStore` interface is the persistence contract. Implement it to plug in any backend.

```go
type JobStore interface {
    // Job lifecycle
    CreateJob(ctx context.Context, params PublishParams) (*Job, error)
    FinalizeJob(ctx context.Context, jobID string, params FinalizeParams) error
    ReportProgress(ctx context.Context, jobID string, p Progress) error

    // Step lifecycle
    StartStep(ctx context.Context, jobID string, params StartStepParams) (*Step, error)
    CompleteStep(ctx context.Context, stepID string, params CompleteStepParams) error
    FailStep(ctx context.Context, stepID string, errMsg string) error

    // Tags
    AddTags(ctx context.Context, jobID string, tags []string) error

    // Queries
    GetJob(ctx context.Context, jobID string) (*Job, error)
    GetJobByExternalReference(ctx context.Context, externalReference string) (*Job, error)
    ListJobs(ctx context.Context, filter ListFilter) ([]Job, error)
    GetSteps(ctx context.Context, jobID string) ([]Step, error)
}
```

The Client handles validation, retries, and Run/Step orchestration. The JobStore only handles persistence.

### Postgres backend

The `postgres/` subpackage provides a ready-to-use implementation backed by Postgres with SQLC-generated queries and goose migrations.

```go
import (
    "github.com/jackc/pgx/v5/pgxpool"
    jobspg "github.com/laenen-partners/jobs/postgres"
)

pool, _ := pgxpool.New(ctx, connString)

// Run migrations (custom scope, or "" for default "jobs").
jobspg.Migrate(ctx, pool, "")

store := jobspg.NewStore(pool)
client := jobs.NewClient(store)
```

### Custom backend

Implement `jobs.JobStore` with your own storage:

```go
type myStore struct { db *sql.DB }

func (s *myStore) CreateJob(ctx context.Context, params jobs.PublishParams) (*jobs.Job, error) {
    // your implementation
}
// ... implement all 11 methods ...

client := jobs.NewClient(&myStore{db: db})
```

Exported helpers available for custom implementations: `ValidateStatus`, `RebuildTags`, `HasAllTags`, `ValidateData`.

## Project structure

```
jobs.go                Pure Go types: Job, Step, Progress, Status, all Params
store.go               JobStore interface (persistence contract)
client.go              Client: orchestration, validation, Run/Step lifecycle
errors.go              Sentinel errors
jobs_test.go           Unit tests with in-memory mock JobStore

postgres/              Postgres backend (implements jobs.JobStore)
  pgstore.go           NewStore(pool) -> jobs.JobStore
  migrate.go           Scoped migrations with embedded SQL
  sqlc.yaml            SQLC code generation config
  db/migrations/       SQL migration files
  db/queries/          SQLC query definitions
  internal/dbgen/      Generated SQLC code (never edit)

Taskfile.yml           Task runner
mise.toml              Tool versions
```

## License

Private — Laenen Partners.
