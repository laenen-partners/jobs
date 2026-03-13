package jobs

import "context"

// JobStore is the persistence interface for job tracking.
//
// Implement this to plug in a custom backend (EntityStore, SQL, in-memory, etc.).
// The Client delegates all persistence to the JobStore and handles orchestration
// (Run/Step lifecycle, retries, validation) independently.
//
// See the postgres subpackage for a Postgres implementation.
type JobStore interface {
	// Job lifecycle
	CreateJob(ctx context.Context, params PublishParams) (*Job, error)
	FinalizeJob(ctx context.Context, jobID string, params FinalizeParams) error
	ReportProgress(ctx context.Context, jobID string, p Progress) error

	// Step lifecycle
	StartStep(ctx context.Context, jobID string, params StartStepParams) (*Step, error)
	CompleteStep(ctx context.Context, stepID string, params CompleteStepParams) error
	FailStep(ctx context.Context, stepID string, errMsg string) error

	// Tags
	AddTags(ctx context.Context, jobID string, tags []string) error

	// Queries
	GetJob(ctx context.Context, jobID string) (*Job, error)
	GetJobByExternalReference(ctx context.Context, externalReference string) (*Job, error)
	ListJobs(ctx context.Context, filter ListFilter) ([]Job, error)
	GetSteps(ctx context.Context, jobID string) ([]Step, error)
}
