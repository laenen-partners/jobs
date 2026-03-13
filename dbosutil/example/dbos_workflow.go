// Package example demonstrates how to use the Jobs SDK with DBOS workflows.
//
// This example shows the new Client interface pattern where Run/Step/Progress
// handle all job lifecycle tracking automatically.
package example

import (
	"context"
	"fmt"
	"net/http"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/laenen-partners/entitystore/gen/entitystore/v1/entitystorev1connect"
	"github.com/laenen-partners/jobs"
)

// ProcessInput is the workflow input.
type ProcessInput struct {
	OwnerID  string   `json:"owner_id"`
	InboxID  string   `json:"inbox_id"`
	InputIDs []string `json:"input_ids"`
}

// Processor holds dependencies for the file processing workflow.
type Processor struct {
	jobs jobs.Client
}

// NewProcessor creates a Processor with a Jobs SDK client connected to EntityStore.
func NewProcessor(entityStoreURL string) *Processor {
	esClient := entitystorev1connect.NewEntityStoreServiceClient(
		http.DefaultClient,
		entityStoreURL,
	)
	store := jobs.NewStore(esClient)
	return &Processor{
		jobs: jobs.NewDefaultClient(store),
	}
}

// ProcessFiles is a DBOS workflow that tracks its lifecycle as a job entity.
//
// The Run method handles:
//   - Publishing the job (best-effort, retried)
//   - Cancelling any existing job with the same workflow ID
//   - Finalizing the job as completed/failed on exit
func (p *Processor) ProcessFiles(ctx dbos.DBOSContext, input ProcessInput) error {
	workflowID, err := dbos.GetWorkflowID(ctx)
	if err != nil {
		return fmt.Errorf("get workflow id: %w", err)
	}

	return p.jobs.Run(ctx, jobs.RunParams{
		WorkflowID: workflowID,
		JobType:    "file_processing",
		OwnerID:    input.OwnerID,
		InputIDs:   input.InputIDs,
		OutputFn:   func() []string { return input.InputIDs },
	}, func(ctx context.Context, rc *jobs.RunContext) error {
		// Process each file as a tracked step.
		for i, inputID := range input.InputIDs {
			if err := p.jobs.Step(ctx, rc, "process_file:"+inputID, func(ctx context.Context) error {
				_, err := dbos.RunAsStep(ctx.(dbos.DBOSContext), func(sctx context.Context) (string, error) {
					return p.processFile(sctx, inputID)
				}, dbos.WithStepName(fmt.Sprintf("process_file_%d", i)))
				return err
			}, jobs.WithSequence(i)); err != nil {
				return err
			}

			// Report progress.
			p.jobs.Progress(ctx, rc, jobs.Progress{
				Step:    "processing",
				Current: i + 1,
				Total:   len(input.InputIDs),
				Message: fmt.Sprintf("Processed %s", inputID),
			})
		}
		return nil
	})
}

// processFile does the actual file processing work.
func (p *Processor) processFile(_ context.Context, inputID string) (string, error) {
	// ... your processing logic here ...
	return "output-" + inputID, nil
}
