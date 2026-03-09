# Jobs SDK

Go SDK for durable job tracking built on top of EntityStore. Provides a high-level abstraction for creating, tracking, and querying jobs across services.

See `~/.claude/CLAUDE.md` for org-wide Go service standards.

## Quick reference

- **Type:** SDK / Go library (not a standalone service)
- **Backing store:** EntityStore (via connect-go client)
- **DBOS integration:** Optional, via `dbosutil` subpackage
- **Entity types:** `jobs.v1.Job` and `jobs.v1.JobStep` in EntityStore (proto-qualified)
- **Proto schema:** `proto/jobs/v1/job.proto` with EntityStore matching annotations
- **BSR dependency:** `buf.build/laenen-partners/entitystore`

## Project structure

```
proto/jobs/v1/
  job.proto            Job and Progress proto definitions with EntityStore annotations
gen/jobs/v1/
  job.pb.go            Generated protobuf Go code (never edit)
  job_entitystore.go   Generated EntityStore match config (never edit)
jobs.go                Core Client, Job type, PublishParams, FinalizeParams
steps.go               Step tracking (StartStep, CompleteStep, FailStep, GetSteps)
jobs_test.go           Tests (mock EntityStore client)
list.go                ListFilter → EntityStore query translation (with pagination)
errors.go              Sentinel errors
dbosutil/
  dbosutil.go          DBOS durable step wrappers (PublishStep, FinalizeStep, ProgressEvent)
buf.yaml               Buf module config with BSR dependency
buf.gen.yaml           Code generation config (protobuf, connect, entitystore)
Taskfile.yml           Task runner commands
mise.toml              Tool versions (go, buf, task)
.github/workflows/
  ci.yml               GitHub Actions CI (lint, build, vet, test)
go.mod                 Depends on: entitystore (connect-go client), optionally dbos
```

## Architecture

```
FileProcessor / Any Service
        │
        ▼
    Jobs SDK (this module)    ← Publish, Finalize, Get, List, StartStep, CompleteStep, ...
        │
        ▼
EntityStore connect-go client ← InsertEntity, UpdateEntity, FindByAnchors, SetTags, ...
        │
        ▼
EntityStore Service           ← Postgres (entities, relations, tags)
```

## Entity model

Jobs are defined as a proto message (`jobs.v1.Job`) with EntityStore annotations:

- **Status enum:** `JobStatus` proto enum (PENDING, RUNNING, COMPLETED, FAILED, CANCELLED)
- **Anchor:** `workflow_id` (exact match, globally unique per workflow)
- **Matching field:** `job_type` (exact, lowercase-trim normalized)
- **Thresholds:** auto_match=0.95, review_zone=0.80
- **Allowed relations:** owned_by, assigned_to, created_from, processes, produces, step_of
- **Size limits:** input/output max 1 MiB, metadata max 64 KiB, step input/output max 256 KiB

Data is serialized via `protojson` for storage in EntityStore's data field.

## Relationships

| Relation | Direction | Purpose |
|---|---|---|
| `owned_by` | Job → User | Who submitted the job |
| `assigned_to` | Job → Team | Which team owns it |
| `created_from` | Job → Inbox | Which inbox template was used |
| `processes` | Job → Entity | Input documents/entities |
| `produces` | Job → Entity | Output documents/entities |
| `step_of` | Step → Job | Step belongs to parent job |

## Tags

Tags map to job lifecycle states for fast filtering:

- `pending`, `running`, `completed`, `failed`, `cancelled` — status
- Additional caller-defined tags passed through

## Safety guarantees

- **Status validation:** Only known `JobStatus` enum values are accepted
- **Terminal state guard:** Cannot re-finalize a completed/failed/cancelled job (`ErrAlreadyFinalized`)
- **Finalize requires terminal status:** Only completed/failed/cancelled accepted
- **Size limits:** Input, Output, Metadata, and step data fields are bounded to prevent unbounded storage
- **Tag consistency:** `SetTags` replaces all tags atomically (no silent swallowing)
- **Audit logging:** All state transitions logged with `slog.InfoContext` including previous status

## Usage

### Registering the match config

```go
import (
    "github.com/laenen-partners/entitystore/matching"
    jobsv1 "github.com/laenen-partners/jobs/gen/jobs/v1"
)

mcr := matching.NewMatchConfigRegistry()
mcr.Register(jobsv1.JobMatchConfig())
mcr.Register(jobsv1.JobStepMatchConfig())
```

### Direct usage (no DBOS)

```go
client := jobs.NewClient(entityStoreClient)

job, err := client.Publish(ctx, jobs.PublishParams{
    JobType:  "file_processing",
    OwnerID:  "user-123",
    InputIDs: []string{"doc-456"},
})

// Lookup by workflow ID (anchor-based).
job, err = client.GetByWorkflowID(ctx, "workflow-uuid")

// Track steps within the job.
step, err := client.StartStep(ctx, job.ID, jobs.StartStepParams{
    Name: "convert", Sequence: 0,
})
// ... do work ...
err = client.CompleteStep(ctx, step.ID, jobs.CompleteStepParams{})

err = client.Finalize(ctx, job.ID, jobs.FinalizeParams{
    Status: jobs.StatusCompleted,
    Output: outputJSON,
})
```

### With DBOS workflows

```go
import "github.com/laenen-partners/jobs/dbosutil"

func (p *Processor) MyWorkflow(ctx dbos.DBOSContext, input Input) (Output, error) {
    job, err := dbosutil.PublishStep(ctx, p.jobs, jobs.PublishParams{...})
    dbos.SetEvent(ctx, "job_entity_id", job.ID)

    defer func() {
        dbosutil.FinalizeStep(ctx, p.jobs, job.ID, jobs.FinalizeParams{...})
    }()

    // ... workflow steps ...
}
```

## Code conventions

- No `init()` functions.
- Errors wrapped with `fmt.Errorf("context: %w", err)`.
- Uses `slog.InfoContext` for structured logging (propagates context).
- EntityStore is the only persistence dependency — no direct DB access.
- Generated code in `gen/` — never edit manually. Regenerate with `task generate`.
