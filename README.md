# Jobs SDK

Go SDK for durable job tracking built on [EntityStore](https://github.com/laenen-partners/entitystore). Provides a high-level abstraction for creating, tracking, and querying long-running jobs across services.

Jobs are stored as EntityStore entities with a proto-defined schema (`jobs.v1.Job`) that includes matching annotations, a status enum, and relationship constraints.

## Install

```sh
go get github.com/laenen-partners/jobs
```

For DBOS workflow integration:

```sh
go get github.com/laenen-partners/jobs/dbosutil
```

## Quick start

```go
import (
    "github.com/laenen-partners/jobs"
    "github.com/laenen-partners/entitystore/gen/entitystore/v1/entitystorev1connect"
)

// Create client.
esClient := entitystorev1connect.NewEntityStoreServiceClient(
    http.DefaultClient,
    "http://localhost:3002",
)
client := jobs.NewClient(esClient)

// Publish a job.
job, err := client.Publish(ctx, jobs.PublishParams{
    WorkflowID: "wf-123",
    JobType:    "file_processing",
    OwnerID:    "user-456",
    InputIDs:   []string{"doc-789"},
})

// Report progress.
err = client.ReportProgress(ctx, job.ID, jobs.Progress{
    Step:    "converting",
    Current: 3,
    Total:   10,
    Message: "Converting page 3 of 10",
})

// Track steps within the job.
step, err := client.StartStep(ctx, job.ID, jobs.StartStepParams{
    Name:     "convert",
    Sequence: 0,
    Input:    []byte(`{"format": "pdf"}`),
})
// ... do work ...
err = client.CompleteStep(ctx, step.ID, jobs.CompleteStepParams{
    Output: []byte(`{"pages": 10}`),
})

// Finalize on completion.
err = client.Finalize(ctx, job.ID, jobs.FinalizeParams{
    Status:    jobs.StatusCompleted,
    Output:    []byte(`{"pages": 10}`),
    OutputIDs: []string{"doc-output-001"},
})
```

## Concepts

A **job** represents a unit of work that one service submits and another (or the same) service executes. The Jobs SDK models the full lifecycle — creation, progress tracking, completion or failure — as an EntityStore entity with typed relationships to the actors and data involved.

### Key identifiers

| Field | What it is | Example |
|---|---|---|
| **Job ID** | The EntityStore entity ID, assigned on creation. Used for all SDK operations (`Get`, `Finalize`, `ReportProgress`). | `"ent-a1b2c3"` |
| **WorkflowID** | A caller-provided identifier that ties the job to an external workflow (typically a DBOS workflow UUID). Stored as an EntityStore **anchor** — guaranteed unique across all jobs. Use `GetByWorkflowID` to look up a job without knowing its entity ID. | `"wf-550e8400-..."` |
| **JobType** | A classification string that describes what kind of work the job performs. Used for filtering (`ListFilter.JobType`) and for humans to understand what a job does. | `"file_processing"`, `"invoice_extraction"`, `"report_generation"` |

### Relationships — who and what is involved

Jobs don't exist in isolation. They are connected to other entities in the graph via directed relationships:

| Concept | Relation | Param field | Description |
|---|---|---|---|
| **Owner** | `owned_by` | `OwnerID` | The user or service account that submitted the job. **Required.** Every job must have an owner for accountability and for querying ("show me my jobs"). |
| **Team** | `assigned_to` | `TeamID` | The team or organization that is responsible for the job. Optional. Useful for multi-tenant systems where jobs need to be scoped to a team. |
| **Inbox** | `created_from` | `InboxID` | The inbox or template that triggered the job. Optional. In document processing pipelines, an inbox defines the extraction rules and destination — linking the job back to it provides traceability. |
| **Inputs** | `processes` | `InputIDs` | The entities that the job will process — documents, files, records, or any other EntityStore entity. These are the "raw materials" the job works on. Set at publish time. |
| **Outputs** | `produces` | `OutputIDs` | The entities that the job created as a result of its work — extracted data, converted files, generated reports. Set at finalize time. |
| **Steps** | `step_of` | *(via `StartStep`)* | Tracked units of work within a job — each step is a separate EntityStore entity linked to the parent job. Steps have their own status, input, output, and timing. |

The input/output model creates a **provenance graph**: you can trace any output entity back through the job that produced it, to the input entities it was derived from, to the user who submitted it and the inbox rules that governed it.

```
User (owner)
  │ owned_by
  ▼
 Job ◄── Inbox (created_from)
  │
  ├── processes ──► Input Doc A
  ├── processes ──► Input Doc B
  │
  ├── produces  ──► Output Entity X
  └── produces  ──► Output Entity Y
  │
  Step A ──step_of──► Job
  Step B ──step_of──► Job
```

**Example:** A file processing job receives two PDF documents (`InputIDs`), extracts structured data from them, and produces two JSON entities (`OutputIDs`). Later, you can query "which job produced Entity X?" or "what inputs were used to create this output?" by traversing the relationship graph.

### Tags — fast filtering

Tags are string labels attached to the job entity for fast filtering. The SDK automatically manages **status tags** (`pending`, `running`, `completed`, `failed`, `cancelled`) — when a job transitions state, the old status tag is replaced atomically.

You can also pass custom tags via `PublishParams.Tags` for your own filtering needs:

```go
job, err := client.Publish(ctx, jobs.PublishParams{
    // ...
    Tags: []string{"priority:high", "env:production", "team:billing"},
})

// Later: find all high-priority completed jobs.
results, err := client.List(ctx, jobs.ListFilter{
    Status: jobs.StatusCompleted,
    Tags:   []string{"priority:high"},
})
```

### Data payloads

| Field | When set | Size limit | Purpose |
|---|---|---|---|
| **Input** (`PublishParams.Input`) | At publish time | 1 MiB | Primary input payload — the data the job will process. Stored in the proto `input` field. |
| **Metadata** (`PublishParams.Metadata`) | At publish time | 64 KiB | Ancillary caller metadata — routing hints, tracing data, configuration. Stored in the proto `metadata` field. |
| **Output** (`FinalizeParams.Output`) | At finalize time | 1 MiB | Primary output payload — summary statistics, extracted fields, results. Stored in the proto `output` field. |

All are `json.RawMessage` (arbitrary JSON bytes). The SDK enforces size limits to prevent unbounded storage growth.

### Progress — tracking in-flight work

Progress reports are optional but recommended for long-running jobs. Each progress update includes:

| Field | Type | Description |
|---|---|---|
| `Step` | `string` | Named phase of the work (e.g. `"downloading"`, `"converting"`, `"extracting"`) |
| `Current` | `int` | How many units are done |
| `Total` | `int` | How many units total |
| `Message` | `string` | Human-readable status message |
| `UpdatedAt` | `time.Time` | Automatically set by the SDK |

The first `ReportProgress` call transitions the job from `pending` to `running`.

## Architecture

```
Your Service (file processor, pipeline, etc.)
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

Jobs are defined as a proto message with EntityStore annotations in [`proto/jobs/v1/job.proto`](proto/jobs/v1/job.proto):

```protobuf
enum JobStatus {
  JOB_STATUS_UNSPECIFIED = 0;
  JOB_STATUS_PENDING     = 1;
  JOB_STATUS_RUNNING     = 2;
  JOB_STATUS_COMPLETED   = 3;
  JOB_STATUS_FAILED      = 4;
  JOB_STATUS_CANCELLED   = 5;
}

message Job {
  string    workflow_id = 1;  // anchor — globally unique
  string    job_type    = 2;  // matching field (exact, lowercase-trim)
  JobStatus status      = 3;  // enum-validated lifecycle state
  string    error       = 4;
  Progress  progress    = 5;
  bytes     input       = 6;  // max 1 MiB
  bytes     output      = 7;  // max 1 MiB
  bytes     metadata    = 8;  // max 64 KiB
}

message JobStep {
  string    name         = 1;  // matching field (exact, lowercase-trim)
  int32     sequence     = 2;  // ordering within parent job
  JobStatus status       = 3;
  string    error        = 4;
  bytes     input        = 5;  // max 256 KiB
  bytes     output       = 6;  // max 256 KiB
  Timestamp started_at   = 7;
  Timestamp completed_at = 8;
}
```

### Registering the match config

If your service runs EntityStore matching, register the generated config:

```go
import (
    "github.com/laenen-partners/entitystore/matching"
    jobsv1 "github.com/laenen-partners/jobs/gen/jobs/v1"
)

registry := matching.NewMatchConfigRegistry()
registry.Register(jobsv1.JobMatchConfig())
registry.Register(jobsv1.JobStepMatchConfig())
```

## API reference

### `NewClient(entities) *Client`

Creates a Jobs SDK client backed by an EntityStore connect-go client.

### `Publish(ctx, params) (*Job, error)`

Creates a new job entity in EntityStore and wires up relationships.

```go
job, err := client.Publish(ctx, jobs.PublishParams{
    WorkflowID: "wf-uuid",           // unique workflow identifier
    JobType:    "file_processing",    // categorization
    OwnerID:    "user-123",           // required — creates owned_by relation
    TeamID:     "team-456",           // optional — creates assigned_to relation
    InboxID:    "inbox-789",          // optional — creates created_from relation
    InputIDs:   []string{"doc-1"},    // optional — creates processes relations
    Tags:       []string{"priority"}, // optional — additional tags
    Input:      inputJSON,            // optional — primary input payload (max 1 MiB)
    Metadata:   metaJSON,             // optional — caller metadata (max 64 KiB)
})
```

The job is created with status `pending` and a corresponding tag.

### `Finalize(ctx, jobID, params) error`

Updates a job to a terminal state. Only accepts `completed`, `failed`, or `cancelled`.

```go
err := client.Finalize(ctx, job.ID, jobs.FinalizeParams{
    Status:    jobs.StatusCompleted,
    Output:    outputJSON,                // max 1 MiB
    OutputIDs: []string{"doc-output-1"},  // creates produces relations
})
```

Returns `ErrAlreadyFinalized` if the job is already in a terminal state.

### `ReportProgress(ctx, jobID, progress) error`

Updates the job's progress and transitions status from `pending` to `running`.

```go
err := client.ReportProgress(ctx, job.ID, jobs.Progress{
    Step:    "converting",
    Current: 3,
    Total:   10,
    Message: "Converting page 3 of 10",
})
```

The `UpdatedAt` timestamp is automatically set.

### `Get(ctx, jobID) (*Job, error)`

Retrieves a job by its EntityStore ID, hydrating all relationships (owner, team, inbox, inputs, outputs).

### `GetByWorkflowID(ctx, workflowID) (*Job, error)`

Retrieves a job by its `workflow_id` anchor using `FindByAnchors`. Returns `ErrNotFound` if no match.

```go
job, err := client.GetByWorkflowID(ctx, "wf-uuid")
```

### `List(ctx, filter) ([]Job, error)`

Queries jobs with relationship, tag, and status filters. Filters are AND-combined.

```go
completed, err := client.List(ctx, jobs.ListFilter{
    OwnerID: "user-123",
    Status:  jobs.StatusCompleted,
    JobType: "file_processing",
    Limit:   25,
    Offset:  0,
})
```

| Filter | Description |
|---|---|
| `OwnerID` | Jobs with `owned_by` relation to this entity |
| `TeamID` | Jobs with `assigned_to` relation to this entity |
| `InboxID` | Jobs with `created_from` relation to this entity |
| `Status` | Filter by status tag (validated against enum) |
| `JobType` | Filter by `job_type` in entity data (client-side) |
| `Tags` | Additional tag filters |
| `Limit` | Max results (default 100) |
| `Offset` | Skip first N results |

### `Cancel(ctx, jobID) error`

Shorthand for `Finalize` with `StatusCancelled`.

### `StartStep(ctx, jobID, params) (*Step, error)`

Creates a new step entity linked to a parent job. The step starts in `running` status with a `started_at` timestamp.

```go
step, err := client.StartStep(ctx, job.ID, jobs.StartStepParams{
    Name:     "convert",       // step name (required)
    Sequence: 0,               // ordering within the job
    Input:    inputJSON,       // optional — max 256 KiB
})
```

### `CompleteStep(ctx, stepID, params) error`

Marks a step as completed with optional output data.

```go
err := client.CompleteStep(ctx, step.ID, jobs.CompleteStepParams{
    Output: outputJSON,  // optional — max 256 KiB
})
```

### `FailStep(ctx, stepID, errorMsg) error`

Marks a step as failed with an error message.

```go
err := client.FailStep(ctx, step.ID, "conversion failed: unsupported format")
```

### `GetSteps(ctx, jobID) ([]Step, error)`

Retrieves all steps linked to a job via `step_of` relations.

```go
steps, err := client.GetSteps(ctx, job.ID)
for _, s := range steps {
    fmt.Printf("Step %d (%s): %s\n", s.Sequence, s.Name, s.Status)
}
```

## Relationships

| Relation | Direction | Purpose |
|---|---|---|
| `owned_by` | Job → User | Who submitted the job |
| `assigned_to` | Job → Team | Which team owns it |
| `created_from` | Job → Inbox | Which inbox template was used |
| `processes` | Job → Entity | Input documents/entities |
| `produces` | Job → Entity | Output documents/entities |
| `step_of` | Step → Job | Step belongs to parent job |

## Status lifecycle

```
          Publish
             │
             ▼
        ┌─────────┐
        │ pending  │
        └────┬─────┘
             │ ReportProgress
             ▼
        ┌─────────┐
        │ running  │
        └──┬───┬───┘
           │   │
    ┌──────┘   └──────┐
    ▼                  ▼
┌───────────┐   ┌──────────┐   ┌───────────┐
│ completed │   │  failed  │   │ cancelled │
└───────────┘   └──────────┘   └───────────┘
```

Terminal states (`completed`, `failed`, `cancelled`) cannot be changed. Attempting to finalize a job that is already in a terminal state returns `ErrAlreadyFinalized`.

## Safety guarantees

- **Status validation** — Only known `JobStatus` enum values are accepted
- **Terminal state guard** — Cannot re-finalize a completed/failed/cancelled job
- **Size limits** — Input/output max 1 MiB, metadata max 64 KiB, step input/output max 256 KiB
- **Atomic tag updates** — `SetTags` replaces all tags in one call
- **Audit logging** — All state transitions logged via `slog.InfoContext` with job ID, status, owner, and previous status

## Errors

| Error | When |
|---|---|
| `ErrNotFound` | Job entity does not exist (`Get`, `GetByWorkflowID`) |
| `ErrAlreadyFinalized` | Job is already completed/failed/cancelled (`Finalize`, `Cancel`) |

Both are sentinel errors suitable for `errors.Is`:

```go
if errors.Is(err, jobs.ErrNotFound) {
    // handle missing job
}
```

## DBOS workflow integration

The `dbosutil` subpackage wraps Jobs SDK methods as DBOS durable steps. This ensures job entity operations are checkpointed and survive workflow recovery — if a workflow crashes and restarts, DBOS replays the stored result without re-executing the EntityStore call.

```sh
go get github.com/laenen-partners/jobs/dbosutil
```

### Step wrappers

| Function | DBOS Step Name | Description |
|---|---|---|
| `PublishStep` | `publish_job` | Creates job entity as a durable step |
| `FinalizeStep` | `finalize_job` | Updates final state as a durable step |
| `ProgressStep` | `job_progress_<step>` | Reports progress as a durable step |
| `ProgressEvent` | *(not a step)* | Lightweight progress via DBOS events |

### Example: File processing workflow

```go
package processor

import (
    "context"
    "fmt"

    "github.com/dbos-inc/dbos-transact-golang/dbos"

    "github.com/laenen-partners/jobs"
    "github.com/laenen-partners/jobs/dbosutil"
)

// Processor holds dependencies for the file processing workflow.
type Processor struct {
    jobs *jobs.Client
}

// ProcessFiles is a DBOS workflow that tracks its lifecycle as a job entity.
func (p *Processor) ProcessFiles(ctx dbos.DBOSContext, input ProcessInput) (ProcessOutput, error) {
    workflowID, _ := dbos.GetWorkflowID(ctx)

    // Publish the job entity (durable step — survives restarts).
    job, err := dbosutil.PublishStep(ctx, p.jobs, jobs.PublishParams{
        WorkflowID: workflowID,
        JobType:    "file_processing",
        OwnerID:    input.OwnerID,
        InboxID:    input.InboxID,
        InputIDs:   input.InputIDs,
    })
    if err != nil {
        return ProcessOutput{}, fmt.Errorf("publish job: %w", err)
    }

    // Expose the job entity ID for external polling.
    _ = dbos.SetEvent(ctx, "job_entity_id", job.ID)

    // Ensure the job is finalized on all exit paths.
    var (
        outputIDs   []string
        finalStatus = jobs.StatusCompleted
        finalErr    string
    )
    defer func() {
        _ = dbosutil.FinalizeStep(ctx, p.jobs, job.ID, jobs.FinalizeParams{
            Status:    finalStatus,
            Error:     finalErr,
            OutputIDs: outputIDs,
        })
    }()

    // Process each input, tracking as job steps.
    for i, inputID := range input.InputIDs {
        // Start a tracked step.
        step, _ := dbos.RunAsStep(ctx, func(sctx context.Context) (*jobs.Step, error) {
            return p.jobs.StartStep(sctx, job.ID, jobs.StartStepParams{
                Name:     "process_file",
                Sequence: i,
            })
        }, dbos.WithStepName(fmt.Sprintf("start_step_%d", i)))

        // Do the actual work.
        outputID, err := dbos.RunAsStep(ctx, func(sctx context.Context) (string, error) {
            return p.processFile(sctx, inputID)
        }, dbos.WithStepName(fmt.Sprintf("process_file_%d", i)))
        if err != nil {
            // Mark step as failed.
            _, _ = dbos.RunAsStep(ctx, func(sctx context.Context) (any, error) {
                return nil, p.jobs.FailStep(sctx, step.ID, err.Error())
            }, dbos.WithStepName(fmt.Sprintf("fail_step_%d", i)))
            finalStatus = jobs.StatusFailed
            finalErr = fmt.Sprintf("process %s: %s", inputID, err)
            return ProcessOutput{}, fmt.Errorf("process file %s: %w", inputID, err)
        }

        // Mark step as completed.
        _, _ = dbos.RunAsStep(ctx, func(sctx context.Context) (any, error) {
            return nil, p.jobs.CompleteStep(sctx, step.ID, jobs.CompleteStepParams{})
        }, dbos.WithStepName(fmt.Sprintf("complete_step_%d", i)))

        outputIDs = append(outputIDs, outputID)
    }

    return ProcessOutput{
        JobID:     job.ID,
        OutputIDs: outputIDs,
    }, nil
}
```

### Polling job status from outside the workflow

Callers that start the workflow can poll for the job entity ID via DBOS events, then use the Jobs SDK to check status:

```go
// Start the workflow.
handle, err := dbos.StartWorkflow(ctx, processor.ProcessFiles, input)
if err != nil {
    return err
}

// Wait for the job entity ID to be available.
jobEntityID, err := dbos.GetEvent[string](ctx, handle.WorkflowID, "job_entity_id", 30*time.Second)
if err != nil {
    return fmt.Errorf("waiting for job entity: %w", err)
}

// Poll the job status via the Jobs SDK.
job, err := jobsClient.Get(ctx, jobEntityID)
fmt.Printf("Job %s is %s (%d/%d)\n", job.ID, job.Status, job.Progress.Current, job.Progress.Total)

// Or look up by workflow ID directly.
job, err = jobsClient.GetByWorkflowID(ctx, handle.WorkflowID)
```

### Lightweight progress (no EntityStore writes)

For high-frequency progress updates where durability isn't needed, use `ProgressEvent` instead of `ProgressStep`. This publishes progress via DBOS's event system without writing to EntityStore:

```go
// Inside the workflow — frequent, lightweight updates.
for i, page := range pages {
    _ = dbosutil.ProgressEvent(ctx, jobs.Progress{
        Step:    "ocr",
        Current: i + 1,
        Total:   len(pages),
    })
    processPage(page)
}

// Outside the workflow — poll progress.
progress, err := dbos.GetEvent[jobs.Progress](ctx, workflowID, "progress", timeout)
```

## Project structure

```
proto/jobs/v1/
  job.proto              Job and Progress proto definitions with EntityStore annotations
gen/jobs/v1/
  job.pb.go              Generated protobuf Go code (never edit)
  job_entitystore.go     Generated EntityStore match config (never edit)
jobs.go                  Core Client, Job type, PublishParams, FinalizeParams
steps.go                 Step tracking (StartStep, CompleteStep, FailStep, GetSteps)
jobs_test.go             Tests (mock EntityStore client)
list.go                  ListFilter with pagination
errors.go                Sentinel errors (ErrNotFound, ErrAlreadyFinalized)
dbosutil/
  dbosutil.go            DBOS durable step wrappers
buf.yaml                 Buf module config (depends on buf.build/laenen-partners/entitystore)
buf.gen.yaml             Code generation (protobuf, connect, protoc-gen-entitystore)
Taskfile.yml             Task runner commands
mise.toml                Tool versions (go, buf, task)
.github/workflows/
  ci.yml                 GitHub Actions CI (lint, build, vet, test)
```

## Development

```sh
mise install          # install tools from mise.toml
task generate         # buf dep update && buf generate
task build            # go build ./...
task test             # go test -v -count=1 ./...
task test:cover       # run tests with coverage
task lint             # buf lint
task tidy             # go mod tidy (all modules)
```

## License

Private — Laenen Partners.
