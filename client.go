package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Client provides job tracking with lifecycle management.
//
// Use NewClient with a JobStore implementation for tracked jobs, or
// NoopClient for a client that records nothing.
//
// The high-level TrackRun/TrackStep methods provide best-effort lifecycle
// tracking with automatic retries. The direct methods (RegisterJob,
// FinalizeJob, GetJob, etc.) give fine-grained control over each operation.
type Client struct {
	store      JobStore
	maxRetries int
	retryDelay time.Duration
	logger     *slog.Logger
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithMaxRetries sets the maximum number of retry attempts for tracking operations.
// Default: 3.
func WithMaxRetries(n int) ClientOption {
	return func(c *Client) { c.maxRetries = n }
}

// WithRetryDelay sets the delay between retry attempts.
// Default: 500ms.
func WithRetryDelay(d time.Duration) ClientOption {
	return func(c *Client) { c.retryDelay = d }
}

// WithLogger sets the logger for tracking error messages.
// Default: slog.Default().
func WithLogger(l *slog.Logger) ClientOption {
	return func(c *Client) { c.logger = l }
}

// NewClient creates a Client backed by a JobStore with retries and best-effort logging.
func NewClient(store JobStore, opts ...ClientOption) *Client {
	c := &Client{
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

// NoopClient creates a Client without job tracking.
// TrackRun and TrackStep still execute work functions; direct operations return ErrNoStore.
func NoopClient() *Client {
	return &Client{logger: slog.Default()}
}

// RunFunc is the work function executed within a tracked run.
type RunFunc func(ctx context.Context, rc *RunContext) error

// StepFunc is the work function executed within a tracked step.
type StepFunc func(ctx context.Context) error

// RunContext carries job state through the execution.
// Passed from TrackRun to TrackStep calls. Use Progress to report in-flight progress.
type RunContext struct {
	JobID  string
	report func(ctx context.Context, p Progress)
}

// Progress reports progress on the current job. Best-effort, never errors.
// No-op when the RunContext has no backing store.
func (rc *RunContext) Progress(ctx context.Context, p Progress) {
	if rc != nil && rc.report != nil {
		rc.report(ctx, p)
	}
}

// RunParams configures a tracked job run.
type RunParams struct {
	ExternalReference string
	JobType           string
	Tags              []string
	OutputFn          func() []string // called on success to resolve output tags; optional
}

// StepConfig holds resolved step options.
type StepConfig struct {
	Retries    int
	Timeout    time.Duration
	Sequence   int
	Extensions map[string]any
}

// StepOption configures step execution.
type StepOption func(*StepConfig)

// WithRetries sets the number of retry attempts for the work function.
func WithRetries(n int) StepOption {
	return func(c *StepConfig) { c.Retries = n }
}

// WithTimeout sets a timeout for the step execution.
func WithTimeout(d time.Duration) StepOption {
	return func(c *StepConfig) { c.Timeout = d }
}

// WithSequence sets the step ordering within a job.
func WithSequence(n int) StepOption {
	return func(c *StepConfig) { c.Sequence = n }
}

// WithExtension sets an engine-specific option.
func WithExtension(key string, value any) StepOption {
	return func(c *StepConfig) {
		if c.Extensions == nil {
			c.Extensions = make(map[string]any)
		}
		c.Extensions[key] = value
	}
}

func applyStepOptions(opts []StepOption) StepConfig {
	cfg := StepConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// --- Direct tracking operations (delegate to Store) ---

// RegisterJob creates a new tracked job.
func (c *Client) RegisterJob(ctx context.Context, params RegisterJobParams) (*Job, error) {
	if c.store == nil {
		return nil, ErrNoStore
	}
	if err := ValidateData(params.Input, MaxInputSize, "input"); err != nil {
		return nil, err
	}
	if err := ValidateData(params.Metadata, MaxMetadataSize, "metadata"); err != nil {
		return nil, err
	}
	return c.store.CreateJob(ctx, params)
}

// FinalizeJob updates a job's final state (completed/failed/cancelled).
func (c *Client) FinalizeJob(ctx context.Context, jobID string, params FinalizeParams) error {
	if c.store == nil {
		return ErrNoStore
	}
	if err := ValidateStatus(params.Status); err != nil {
		return err
	}
	if !TerminalStatuses[params.Status] {
		return fmt.Errorf("jobs: finalize requires a terminal status (completed, failed, cancelled), got %q", params.Status)
	}
	if err := ValidateData(params.Output, MaxOutputSize, "output"); err != nil {
		return err
	}
	return c.store.FinalizeJob(ctx, jobID, params)
}

// ReportProgress updates the job's progress field.
func (c *Client) ReportProgress(ctx context.Context, jobID string, p Progress) error {
	if c.store == nil {
		return ErrNoStore
	}
	return c.store.ReportProgress(ctx, jobID, p)
}

// CancelJob marks a job as cancelled.
func (c *Client) CancelJob(ctx context.Context, jobID string) error {
	return c.FinalizeJob(ctx, jobID, FinalizeParams{
		Status: StatusCancelled,
	})
}

// GetJob retrieves a job by ID.
func (c *Client) GetJob(ctx context.Context, jobID string) (*Job, error) {
	if c.store == nil {
		return nil, ErrNoStore
	}
	return c.store.GetJob(ctx, jobID)
}

// GetByExternalReference retrieves a job by its external reference.
func (c *Client) GetByExternalReference(ctx context.Context, externalReference string) (*Job, error) {
	if c.store == nil {
		return nil, ErrNoStore
	}
	return c.store.GetJobByExternalReference(ctx, externalReference)
}

// ListJobs queries jobs using tag filters.
func (c *Client) ListJobs(ctx context.Context, filter ListFilter) ([]Job, error) {
	if c.store == nil {
		return nil, ErrNoStore
	}
	return c.store.ListJobs(ctx, filter)
}

// RegisterStep creates a new tracked step linked to a parent job.
func (c *Client) RegisterStep(ctx context.Context, jobID string, params RegisterStepParams) (*Step, error) {
	if c.store == nil {
		return nil, ErrNoStore
	}
	if params.Name == "" {
		return nil, fmt.Errorf("jobs: step name is required")
	}
	if err := ValidateData(params.Input, MaxStepInputSize, "step input"); err != nil {
		return nil, err
	}
	return c.store.CreateStep(ctx, jobID, params)
}

// CompleteStep marks a step as completed with optional output data.
func (c *Client) CompleteStep(ctx context.Context, stepID string, params CompleteStepParams) error {
	if c.store == nil {
		return ErrNoStore
	}
	if err := ValidateData(params.Output, MaxStepOutputSize, "step output"); err != nil {
		return err
	}
	return c.store.CompleteStep(ctx, stepID, params)
}

// FailStep marks a step as failed with an error message.
func (c *Client) FailStep(ctx context.Context, stepID string, stepErr string) error {
	if c.store == nil {
		return ErrNoStore
	}
	return c.store.FailStep(ctx, stepID, stepErr)
}

// GetSteps retrieves all steps linked to a job.
func (c *Client) GetSteps(ctx context.Context, jobID string) ([]Step, error) {
	if c.store == nil {
		return nil, ErrNoStore
	}
	return c.store.GetSteps(ctx, jobID)
}

// AddTags appends tags to a job without changing its status.
func (c *Client) AddTags(ctx context.Context, jobID string, tags []string) error {
	if c.store == nil {
		return ErrNoStore
	}
	return c.store.AddTags(ctx, jobID, tags)
}

// --- Orchestrated tracking ---

// TrackRun executes fn and tracks it as a job:
//  1. Register job (best-effort, retried)
//  2. Execute fn
//  3. Finalize as completed/failed (best-effort, retried, background context)
//
// When the client has no backing store, fn still executes without tracking.
func (c *Client) TrackRun(ctx context.Context, params RunParams, fn RunFunc) error {
	rc := &RunContext{}

	if c.store != nil {
		c.cancelExisting(ctx, params.ExternalReference)

		var job *Job
		err := c.retry(ctx, "register_job", func(rctx context.Context) error {
			var regErr error
			job, regErr = c.RegisterJob(rctx, RegisterJobParams{
				ExternalReference: params.ExternalReference,
				JobType:           params.JobType,
				Tags:              params.Tags,
			})
			return regErr
		})
		if err != nil {
			c.logger.ErrorContext(ctx, "JOBS: failed to register job — continuing without tracking",
				"external_reference", params.ExternalReference,
				"error", err,
			)
		} else {
			rc.JobID = job.ID
			rc.report = func(ctx context.Context, p Progress) {
				_ = c.retry(ctx, "progress", func(rctx context.Context) error {
					return c.ReportProgress(rctx, rc.JobID, p)
				})
			}
		}
	}

	workErr := fn(ctx, rc)

	if c.store != nil && rc.JobID != "" {
		status := StatusCompleted
		var errMsg string
		var finalizeTags []string
		if workErr != nil {
			status = StatusFailed
			errMsg = workErr.Error()
		} else if params.OutputFn != nil {
			finalizeTags = params.OutputFn()
		}
		_ = c.retry(context.Background(), "finalize_job", func(rctx context.Context) error {
			err := c.FinalizeJob(rctx, rc.JobID, FinalizeParams{
				Status: status,
				Error:  errMsg,
				Tags:   finalizeTags,
			})
			if errors.Is(err, ErrAlreadyFinalized) {
				return nil
			}
			return err
		})
	}

	return workErr
}

// TrackStep executes fn and tracks it as a step on a parent job:
//  1. Register step (best-effort, retried)
//  2. Execute fn
//  3. Complete or fail step (best-effort, retried, background context)
//
// When rc is nil or has no JobID, fn still executes without tracking.
func (c *Client) TrackStep(ctx context.Context, rc *RunContext, name string, fn StepFunc, opts ...StepOption) error {
	cfg := applyStepOptions(opts)
	var stepID string

	if c.store != nil && rc != nil && rc.JobID != "" {
		err := c.retry(ctx, "register_step:"+name, func(rctx context.Context) error {
			step, regErr := c.RegisterStep(rctx, rc.JobID, RegisterStepParams{
				Name:     name,
				Sequence: cfg.Sequence,
			})
			if regErr != nil {
				return regErr
			}
			stepID = step.ID
			return nil
		})
		if err != nil {
			c.logger.ErrorContext(ctx, "JOBS: failed to register step — continuing without step tracking",
				"job_id", rc.JobID,
				"step_name", name,
				"error", err,
			)
		}
	}

	workCtx := ctx
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		workCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	workErr := fn(workCtx)

	if c.store != nil && stepID != "" {
		if workErr != nil {
			_ = c.retry(context.Background(), "fail_step:"+name, func(rctx context.Context) error {
				return c.FailStep(rctx, stepID, workErr.Error())
			})
		} else {
			_ = c.retry(context.Background(), "complete_step:"+name, func(rctx context.Context) error {
				return c.CompleteStep(rctx, stepID, CompleteStepParams{})
			})
		}
	}

	return workErr
}

// retry executes fn up to maxRetries+1 times with retryDelay between attempts.
func (c *Client) retry(ctx context.Context, operation string, fn func(ctx context.Context) error) error {
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

// cancelExisting cancels any existing job with the given external reference.
func (c *Client) cancelExisting(ctx context.Context, externalReference string) {
	if externalReference == "" {
		return
	}
	existing, err := c.GetByExternalReference(ctx, externalReference)
	if err != nil {
		return
	}
	if TerminalStatuses[existing.Status] {
		return
	}
	if err := c.CancelJob(ctx, existing.ID); err != nil {
		c.logger.WarnContext(ctx, "JOBS: failed to cancel existing job",
			"job_id", existing.ID,
			"external_reference", externalReference,
			"error", err,
		)
	} else {
		c.logger.InfoContext(ctx, "JOBS: cancelled existing job",
			"job_id", existing.ID,
			"external_reference", externalReference,
		)
	}
}
