// Package dbosutil provides durable DBOS step wrappers for the Jobs SDK.
//
// These helpers wrap jobs.Client methods as DBOS steps, ensuring that job
// entity operations are checkpointed and survive workflow recovery.
//
// This is a separate module to keep the core jobs package free of the DBOS
// dependency — services that don't use DBOS can still use jobs.Client directly.
package dbosutil

import (
	"context"
	"fmt"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/laenen-partners/jobs"
)

// PublishStep creates a job entity as a durable DBOS step.
// The step is named "publish_job" and is idempotent on recovery — DBOS replays
// the stored result without re-executing.
func PublishStep(ctx dbos.DBOSContext, client *jobs.Store, params jobs.PublishParams) (*jobs.Job, error) {
	job, err := dbos.RunAsStep(ctx, func(sctx context.Context) (*jobs.Job, error) {
		return client.Publish(sctx, params)
	}, dbos.WithStepName("publish_job"))
	if err != nil {
		return nil, fmt.Errorf("dbosutil: publish job: %w", err)
	}
	return job, nil
}

// FinalizeStep updates a job's final state as a durable DBOS step.
// The step is named "finalize_job". This should typically be called from
// a defer block to ensure it runs on both success and failure paths.
func FinalizeStep(ctx dbos.DBOSContext, client *jobs.Store, jobID string, params jobs.FinalizeParams) error {
	_, err := dbos.RunAsStep(ctx, func(sctx context.Context) (any, error) {
		return nil, client.Finalize(sctx, jobID, params)
	}, dbos.WithStepName("finalize_job"))
	if err != nil {
		return fmt.Errorf("dbosutil: finalize job: %w", err)
	}
	return nil
}

// ProgressStep updates job progress as a durable DBOS step.
// Named "job_progress_<step>" for traceability in DBOS history.
func ProgressStep(ctx dbos.DBOSContext, client *jobs.Store, jobID string, p jobs.Progress) error {
	_, err := dbos.RunAsStep(ctx, func(sctx context.Context) (any, error) {
		return nil, client.ReportProgress(sctx, jobID, p)
	}, dbos.WithStepName("job_progress_"+p.Step))
	if err != nil {
		return fmt.Errorf("dbosutil: report progress: %w", err)
	}
	return nil
}

// ProgressEvent publishes progress via DBOS SetEvent without persisting to EntityStore.
// This is lightweight — use it for frequent updates where durability isn't needed.
// Callers retrieve progress via dbos.GetEvent(ctx, workflowID, "progress", timeout).
func ProgressEvent(ctx dbos.DBOSContext, p jobs.Progress) error {
	return dbos.SetEvent(ctx, "progress", p)
}
