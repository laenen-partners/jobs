package jobs

import (
	"context"
	"time"
)

// Client is the high-level interface for running work with job tracking.
// Implementations project job lifecycle events to a store (e.g. EntityStore)
// while executing the actual work. Different workflow engines (DBOS, Temporal)
// provide their own implementations, all projecting to the same store.
//
// All tracking is best-effort — tracking failures never fail the actual work.
// When the client has no backing store (e.g. NoopClient), work still executes.
type Client interface {
	// Run executes fn within a job lifecycle:
	//   1. Publish job (best-effort, retried)
	//   2. Execute fn
	//   3. Finalize as completed/failed (best-effort, retried, background context)
	//
	// The RunContext passed to fn carries the job ID for child Step/Progress calls.
	Run(ctx context.Context, params RunParams, fn RunFunc) error

	// Step executes fn within a step lifecycle on a parent job:
	//   1. Start step (best-effort, retried)
	//   2. Execute fn
	//   3. Complete or fail step (best-effort, retried, background context)
	//
	// When rc is nil or has no JobID, fn still executes without tracking.
	Step(ctx context.Context, rc *RunContext, name string, fn StepFunc, opts ...StepOption) error

	// Progress reports progress on the current job. Best-effort, never errors.
	Progress(ctx context.Context, rc *RunContext, p Progress)
}

// RunFunc is the work function executed within a job Run.
type RunFunc func(ctx context.Context, rc *RunContext) error

// StepFunc is the work function executed within a Step.
type StepFunc func(ctx context.Context) error

// RunContext carries job state through the execution.
// Passed from Run to Step/Progress calls.
type RunContext struct {
	JobID string
}

// RunParams configures a tracked job run.
type RunParams struct {
	WorkflowID string
	JobType    string
	OwnerID    string
	InputIDs   []string
	Tags       []string
	OutputFn   func() []string // called on success to resolve output IDs; optional
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

// WithExtension sets an engine-specific option. Each implementation
// checks for its own keys and ignores the rest.
func WithExtension(key string, value any) StepOption {
	return func(c *StepConfig) {
		if c.Extensions == nil {
			c.Extensions = make(map[string]any)
		}
		c.Extensions[key] = value
	}
}

// applyStepOptions resolves step options into a StepConfig.
func applyStepOptions(opts []StepOption) StepConfig {
	cfg := StepConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}
