package jobs

import (
	"context"
	"slices"
)

// ScopeConfig defines tag-based read/write filtering for a ScopedClient.
// Aligns with entitystore's ScopeConfig pattern.
type ScopeConfig struct {
	// RequireTags filters reads — jobs must carry ALL of these tags (AND semantics).
	RequireTags []string

	// ExcludeTag hides jobs carrying this tag from read results,
	// unless the job also carries one of the UnlessTags.
	ExcludeTag string

	// UnlessTags exempts jobs from ExcludeTag filtering.
	UnlessTags []string

	// AutoTags are automatically added to every job created
	// through this scoped client.
	AutoTags []string
}

// ScopedClient wraps a Client with tag-based read/write filtering.
// All read queries are filtered by RequireTags and ExcludeTag/UnlessTags.
// All job creates are auto-tagged with AutoTags.
type ScopedClient struct {
	inner *Client
	cfg   ScopeConfig
}

// Scoped returns a new ScopedClient that applies tag-based filtering
// to all read and write operations.
func (c *Client) Scoped(cfg ScopeConfig) *ScopedClient {
	return &ScopedClient{inner: c, cfg: cfg}
}

// --- Reads ---

// ListJobs queries jobs using tag filters merged with the scope.
func (s *ScopedClient) ListJobs(ctx context.Context, filter ListFilter) ([]Job, error) {
	jobs, err := s.inner.ListJobs(ctx, s.mergeFilter(filter))
	if err != nil {
		return nil, err
	}
	if s.cfg.ExcludeTag != "" {
		jobs = s.filterVisible(jobs)
	}
	return jobs, nil
}

// ListTags returns distinct tags across jobs matching the scoped filter.
func (s *ScopedClient) ListTags(ctx context.Context, filter ListFilter) ([]string, error) {
	return s.inner.ListTags(ctx, s.mergeFilter(filter))
}

// GetJob retrieves a job by ID. Returns ErrAccessDenied if the job
// exists but is not visible in the current scope.
func (s *ScopedClient) GetJob(ctx context.Context, jobID string) (*Job, error) {
	job, err := s.inner.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if !s.jobVisible(job) {
		return nil, ErrAccessDenied
	}
	return job, nil
}

// GetByExternalReference retrieves a job by its external reference.
// Returns ErrAccessDenied if the job is not visible in the current scope.
func (s *ScopedClient) GetByExternalReference(ctx context.Context, externalReference string) (*Job, error) {
	job, err := s.inner.GetByExternalReference(ctx, externalReference)
	if err != nil {
		return nil, err
	}
	if !s.jobVisible(job) {
		return nil, ErrAccessDenied
	}
	return job, nil
}

// GetSteps retrieves all steps for a job. Verifies the parent job
// is visible in the current scope before returning steps.
func (s *ScopedClient) GetSteps(ctx context.Context, jobID string) ([]Step, error) {
	if _, err := s.GetJob(ctx, jobID); err != nil {
		return nil, err
	}
	return s.inner.GetSteps(ctx, jobID)
}

// --- Writes ---

// RegisterJob creates a new tracked job with AutoTags appended.
func (s *ScopedClient) RegisterJob(ctx context.Context, params RegisterJobParams) (*Job, error) {
	if len(s.cfg.AutoTags) > 0 {
		params.Tags = append(append([]string{}, params.Tags...), s.cfg.AutoTags...)
	}
	return s.inner.RegisterJob(ctx, params)
}

// CancelJob marks a job as cancelled.
func (s *ScopedClient) CancelJob(ctx context.Context, jobID string) error {
	return s.inner.CancelJob(ctx, jobID)
}

// --- Pass-through ---

// FinalizeJob updates a job's final state.
func (s *ScopedClient) FinalizeJob(ctx context.Context, jobID string, params FinalizeParams) error {
	return s.inner.FinalizeJob(ctx, jobID, params)
}

// ReportProgress updates the job's progress field.
func (s *ScopedClient) ReportProgress(ctx context.Context, jobID string, p Progress) error {
	return s.inner.ReportProgress(ctx, jobID, p)
}

// RegisterStep creates a new tracked step linked to a parent job.
func (s *ScopedClient) RegisterStep(ctx context.Context, jobID string, params RegisterStepParams) (*Step, error) {
	return s.inner.RegisterStep(ctx, jobID, params)
}

// CompleteStep marks a step as completed.
func (s *ScopedClient) CompleteStep(ctx context.Context, stepID string, params CompleteStepParams) error {
	return s.inner.CompleteStep(ctx, stepID, params)
}

// FailStep marks a step as failed.
func (s *ScopedClient) FailStep(ctx context.Context, stepID string, stepErr string) error {
	return s.inner.FailStep(ctx, stepID, stepErr)
}

// AddTags appends tags to a job.
func (s *ScopedClient) AddTags(ctx context.Context, jobID string, tags []string) error {
	return s.inner.AddTags(ctx, jobID, tags)
}

// TrackRun executes fn and tracks it as a job with AutoTags appended.
func (s *ScopedClient) TrackRun(ctx context.Context, params RunParams, fn RunFunc) error {
	if len(s.cfg.AutoTags) > 0 {
		params.Tags = append(append([]string{}, params.Tags...), s.cfg.AutoTags...)
	}
	return s.inner.TrackRun(ctx, params, fn)
}

// TrackStep executes fn and tracks it as a step on a parent job.
func (s *ScopedClient) TrackStep(ctx context.Context, rc *RunContext, name string, fn StepFunc, opts ...StepOption) error {
	return s.inner.TrackStep(ctx, rc, name, fn, opts...)
}

// --- Internal ---

func (s *ScopedClient) mergeFilter(filter ListFilter) ListFilter {
	merged := filter
	merged.Tags = append(append([]string{}, filter.Tags...), s.cfg.RequireTags...)
	return merged
}

func (s *ScopedClient) jobVisible(j *Job) bool {
	if !HasAllTags(j.Tags, s.cfg.RequireTags) {
		return false
	}
	if s.cfg.ExcludeTag != "" && slices.Contains(j.Tags, s.cfg.ExcludeTag) {
		for _, u := range s.cfg.UnlessTags {
			if slices.Contains(j.Tags, u) {
				return true
			}
		}
		return false
	}
	return true
}

func (s *ScopedClient) filterVisible(jobs []Job) []Job {
	filtered := make([]Job, 0, len(jobs))
	for i := range jobs {
		if s.jobVisible(&jobs[i]) {
			filtered = append(filtered, jobs[i])
		}
	}
	return filtered
}
