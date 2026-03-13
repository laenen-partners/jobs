package jobs

import "context"

// noopClient implements Client without any tracking.
// Work functions still execute — only the projection is skipped.
type noopClient struct{}

// NewNoopClient creates a Client that executes work without job tracking.
// Useful for tests and environments where no tracking is needed.
func NewNoopClient() Client {
	return &noopClient{}
}

func (n *noopClient) Run(ctx context.Context, params RunParams, fn RunFunc) error {
	return fn(ctx, &RunContext{})
}

func (n *noopClient) Step(ctx context.Context, rc *RunContext, name string, fn StepFunc, opts ...StepOption) error {
	cfg := applyStepOptions(opts)
	workCtx := ctx
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		workCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}
	return fn(workCtx)
}

func (n *noopClient) Progress(_ context.Context, _ *RunContext, _ Progress) {}
