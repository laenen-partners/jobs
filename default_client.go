package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// defaultClient implements Client using a Store for projection.
// It handles retries and best-effort error logging for all tracking operations.
type defaultClient struct {
	store      *Store
	maxRetries int
	retryDelay time.Duration
	logger     *slog.Logger
}

// ClientOption configures a default Client.
type ClientOption func(*defaultClient)

// WithMaxRetries sets the maximum number of retry attempts for tracking operations.
// Default: 3.
func WithMaxRetries(n int) ClientOption {
	return func(c *defaultClient) { c.maxRetries = n }
}

// WithRetryDelay sets the delay between retry attempts.
// Default: 500ms.
func WithRetryDelay(d time.Duration) ClientOption {
	return func(c *defaultClient) { c.retryDelay = d }
}

// WithLogger sets the logger for tracking error messages.
// Default: slog.Default().
func WithLogger(l *slog.Logger) ClientOption {
	return func(c *defaultClient) { c.logger = l }
}

// NewDefaultClient creates a Client backed by a Store with retries and best-effort logging.
// Pass a nil store to get a client where work executes but tracking is skipped.
func NewDefaultClient(store *Store, opts ...ClientOption) Client {
	c := &defaultClient{
		store:      store,
		maxRetries: 3,
		retryDelay: 500 * time.Millisecond,
		logger:     slog.Default(),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *defaultClient) Run(ctx context.Context, params RunParams, fn RunFunc) error {
	rc := &RunContext{}

	// 1. Publish (best-effort with retries).
	if c.store != nil {
		cancelExisting(ctx, c, params.WorkflowID)

		var job *Job
		err := c.retry(ctx, "publish_job", func(rctx context.Context) error {
			var pubErr error
			job, pubErr = c.store.Publish(rctx, PublishParams{
				WorkflowID: params.WorkflowID,
				JobType:    params.JobType,
				OwnerID:    params.OwnerID,
				InputIDs:   params.InputIDs,
				Tags:       params.Tags,
			})
			return pubErr
		})
		if err != nil {
			c.logger.ErrorContext(ctx, "JOBS: failed to publish job — continuing without tracking",
				"workflow_id", params.WorkflowID,
				"error", err,
			)
		} else {
			rc.JobID = job.ID
		}
	}

	// 2. Execute the work.
	workErr := fn(ctx, rc)

	// 3. Finalize (best-effort, background context, with retries).
	if c.store != nil && rc.JobID != "" {
		status := StatusCompleted
		var errMsg string
		var outputIDs []string
		if workErr != nil {
			status = StatusFailed
			errMsg = workErr.Error()
		} else if params.OutputFn != nil {
			outputIDs = params.OutputFn()
		}
		_ = c.retry(context.Background(), "finalize_job", func(rctx context.Context) error {
			err := c.store.Finalize(rctx, rc.JobID, FinalizeParams{
				Status:    status,
				Error:     errMsg,
				OutputIDs: outputIDs,
			})
			// Already finalized is success (idempotent).
			if errors.Is(err, ErrAlreadyFinalized) {
				return nil
			}
			return err
		})
	}

	return workErr
}

func (c *defaultClient) Step(ctx context.Context, rc *RunContext, name string, fn StepFunc, opts ...StepOption) error {
	cfg := applyStepOptions(opts)
	var stepID string

	// 1. StartStep (best-effort with retries).
	if c.store != nil && rc != nil && rc.JobID != "" {
		err := c.retry(ctx, "start_step:"+name, func(rctx context.Context) error {
			step, startErr := c.store.StartStep(rctx, rc.JobID, StartStepParams{
				Name:     name,
				Sequence: cfg.Sequence,
			})
			if startErr != nil {
				return startErr
			}
			stepID = step.ID
			return nil
		})
		if err != nil {
			c.logger.ErrorContext(ctx, "JOBS: failed to start step — continuing without step tracking",
				"job_id", rc.JobID,
				"step_name", name,
				"error", err,
			)
		}
	}

	// 2. Execute the work (with optional timeout).
	workCtx := ctx
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		workCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	workErr := fn(workCtx)

	// 3. Complete or fail (best-effort, background context, with retries).
	if c.store != nil && stepID != "" {
		if workErr != nil {
			_ = c.retry(context.Background(), "fail_step:"+name, func(rctx context.Context) error {
				return c.store.FailStep(rctx, stepID, workErr.Error())
			})
		} else {
			_ = c.retry(context.Background(), "complete_step:"+name, func(rctx context.Context) error {
				return c.store.CompleteStep(rctx, stepID, CompleteStepParams{})
			})
		}
	}

	return workErr
}

func (c *defaultClient) Progress(ctx context.Context, rc *RunContext, p Progress) {
	if c.store == nil || rc == nil || rc.JobID == "" {
		return
	}
	_ = c.retry(ctx, "progress", func(rctx context.Context) error {
		return c.store.ReportProgress(rctx, rc.JobID, p)
	})
}

// retry executes fn up to maxRetries+1 times with retryDelay between attempts.
// On final failure, logs at ERROR level and returns the error.
func (c *defaultClient) retry(ctx context.Context, operation string, fn func(ctx context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.retryDelay):
			}
		}
		if err := fn(ctx); err != nil {
			lastErr = err
			c.logger.WarnContext(ctx, "JOBS: tracking operation failed, retrying",
				"operation", operation,
				"attempt", attempt+1,
				"max_retries", c.maxRetries,
				"error", err,
			)
			continue
		}
		return nil
	}
	c.logger.ErrorContext(ctx, "JOBS: tracking operation failed after all retries — job state may be stale",
		"operation", operation,
		"attempts", c.maxRetries+1,
		"error", lastErr,
	)
	return lastErr
}

// cancelExisting cancels any existing job with the given workflow ID.
// Best-effort — errors are logged but not returned.
func cancelExisting(ctx context.Context, c *defaultClient, workflowID string) {
	if workflowID == "" {
		return
	}
	existing, err := c.store.GetByWorkflowID(ctx, workflowID)
	if err != nil {
		return // not found — nothing to cancel
	}
	if terminalStatuses[existing.Status] {
		return
	}
	if err := c.store.Cancel(ctx, existing.ID); err != nil {
		c.logger.WarnContext(ctx, "JOBS: failed to cancel existing job",
			"job_id", existing.ID,
			"workflow_id", workflowID,
			"error", err,
		)
	} else {
		c.logger.InfoContext(ctx, "JOBS: cancelled existing job",
			"job_id", existing.ID,
			"workflow_id", workflowID,
		)
	}
}
