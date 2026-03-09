// Package example demonstrates how to use the Jobs SDK with DBOS workflows.
//
// This example implements a file processing pipeline where:
//   - A DBOS workflow orchestrates the processing steps
//   - The Jobs SDK tracks the job lifecycle in EntityStore
//   - Progress is reported via durable steps
//   - The job is finalized as completed or failed on all exit paths
//
// To use this in your own service, copy this pattern and replace the
// processFile function with your actual processing logic.
package example

import (
	"context"
	"fmt"
	"net/http"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/laenen-partners/entitystore/gen/entitystore/v1/entitystorev1connect"
	"github.com/laenen-partners/jobs"
	"github.com/laenen-partners/jobs/dbosutil"
)

// ProcessInput is the workflow input.
type ProcessInput struct {
	OwnerID  string   `json:"owner_id"`
	InboxID  string   `json:"inbox_id"`
	InputIDs []string `json:"input_ids"`
}

// ProcessOutput is the workflow result.
type ProcessOutput struct {
	JobID     string   `json:"job_id"`
	OutputIDs []string `json:"output_ids"`
}

// Processor holds dependencies for the file processing workflow.
type Processor struct {
	jobs *jobs.Client
}

// NewProcessor creates a Processor with a Jobs SDK client connected to EntityStore.
func NewProcessor(entityStoreURL string) *Processor {
	esClient := entitystorev1connect.NewEntityStoreServiceClient(
		http.DefaultClient,
		entityStoreURL,
	)
	return &Processor{
		jobs: jobs.NewClient(esClient),
	}
}

// ProcessFiles is a DBOS workflow that tracks its lifecycle as a job entity.
//
// The workflow:
//  1. Publishes a job entity (durable step — survives restarts)
//  2. Exposes the job entity ID as a DBOS event for external polling
//  3. Processes each input file, reporting progress
//  4. Finalizes the job as completed or failed
func (p *Processor) ProcessFiles(ctx dbos.DBOSContext, input ProcessInput) (ProcessOutput, error) {
	// Step 1: Publish the job entity.
	// This is a durable step — if the workflow restarts, DBOS replays
	// the stored result instead of creating a duplicate job.
	workflowID, err := dbos.GetWorkflowID(ctx)
	if err != nil {
		return ProcessOutput{}, fmt.Errorf("get workflow id: %w", err)
	}

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

	// Step 2: Expose the job entity ID so callers can poll status.
	// External services call: dbos.GetEvent(ctx, workflowID, "job_entity_id", timeout)
	if err := dbos.SetEvent(ctx, "job_entity_id", job.ID); err != nil {
		return ProcessOutput{}, fmt.Errorf("set job event: %w", err)
	}

	// Step 3: Ensure the job is finalized on all exit paths.
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

	// Step 4: Process each input, tracking as job steps.
	for i, inputID := range input.InputIDs {
		// Start a tracked step for this input.
		step, err := dbos.RunAsStep(ctx, func(sctx context.Context) (*jobs.Step, error) {
			return p.jobs.StartStep(sctx, job.ID, jobs.StartStepParams{
				Name:     "process_file",
				Sequence: i,
				Input:    fmt.Appendf(nil, `{"input_id":%q}`, inputID),
			})
		}, dbos.WithStepName(fmt.Sprintf("start_step_%d", i)))
		if err != nil {
			finalStatus = jobs.StatusFailed
			finalErr = fmt.Sprintf("start step %d: %s", i, err)
			return ProcessOutput{}, err
		}

		// Report progress.
		if err := dbosutil.ProgressStep(ctx, p.jobs, job.ID, jobs.Progress{
			Step:    "processing",
			Current: i + 1,
			Total:   len(input.InputIDs),
			Message: fmt.Sprintf("Processing %s", inputID),
		}); err != nil {
			finalStatus = jobs.StatusFailed
			finalErr = fmt.Sprintf("progress report: %s", err)
			return ProcessOutput{}, err
		}

		// Do the actual work (as a durable step).
		outputID, err := dbos.RunAsStep(ctx, func(sctx context.Context) (string, error) {
			return p.processFile(sctx, inputID)
		}, dbos.WithStepName(fmt.Sprintf("process_file_%d", i)))
		if err != nil {
			// Mark the step as failed.
			_, _ = dbos.RunAsStep(ctx, func(sctx context.Context) (any, error) {
				return nil, p.jobs.FailStep(sctx, step.ID, err.Error())
			}, dbos.WithStepName(fmt.Sprintf("fail_step_%d", i)))
			finalStatus = jobs.StatusFailed
			finalErr = fmt.Sprintf("process %s: %s", inputID, err)
			return ProcessOutput{}, fmt.Errorf("process file %s: %w", inputID, err)
		}

		// Mark the step as completed.
		_, _ = dbos.RunAsStep(ctx, func(sctx context.Context) (any, error) {
			return nil, p.jobs.CompleteStep(sctx, step.ID, jobs.CompleteStepParams{
				Output: fmt.Appendf(nil, `{"output_id":%q}`, outputID),
			})
		}, dbos.WithStepName(fmt.Sprintf("complete_step_%d", i)))

		outputIDs = append(outputIDs, outputID)
	}

	return ProcessOutput{
		JobID:     job.ID,
		OutputIDs: outputIDs,
	}, nil
}

// processFile does the actual file processing work.
// Replace this with your real processing logic.
func (p *Processor) processFile(_ context.Context, inputID string) (string, error) {
	// ... your processing logic here ...
	// Returns the output entity ID.
	return "output-" + inputID, nil
}
