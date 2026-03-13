// Package postgres implements jobs.JobStore backed by Postgres.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/laenen-partners/jobs"
	"github.com/laenen-partners/jobs/postgres/internal/dbgen"
)

// Store implements jobs.JobStore backed by Postgres via SQLC-generated queries.
type Store struct {
	q *dbgen.Queries
}

// NewStore creates a jobs.JobStore backed by Postgres.
func NewStore(pool *pgxpool.Pool) jobs.JobStore {
	return &Store{q: dbgen.New(pool)}
}

func (s *Store) CreateJob(ctx context.Context, params jobs.RegisterJobParams) (*jobs.Job, error) {
	tags := append([]string{string(jobs.StatusPending)}, params.Tags...)

	row, err := s.q.CreateJob(ctx, dbgen.CreateJobParams{
		ExternalReference: params.ExternalReference,
		JobType:           params.JobType,
		Status:            string(jobs.StatusPending),
		Input:             params.Input,
		Metadata:          params.Metadata,
		Tags:              tags,
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: create: %w", err)
	}
	return rowToJob(row), nil
}

func (s *Store) FinalizeJob(ctx context.Context, jobID string, params jobs.FinalizeParams) error {
	id, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("jobs: invalid job id: %w", err)
	}

	// Get current tags for rebuilding.
	currentTags, err := s.q.GetJobTags(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return jobs.ErrNotFound
		}
		return fmt.Errorf("jobs: get tags: %w", err)
	}

	finalTags := jobs.RebuildTags(currentTags, params.Status)
	finalTags = append(finalTags, params.Tags...)

	rows, err := s.q.FinalizeJob(ctx, dbgen.FinalizeJobParams{
		ID:     id,
		Status: string(params.Status),
		Error:  params.Error,
		Output: params.Output,
		Tags:   finalTags,
	})
	if err != nil {
		return fmt.Errorf("jobs: finalize: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: job %s", jobs.ErrAlreadyFinalized, jobID)
	}
	return nil
}

func (s *Store) ReportProgress(ctx context.Context, jobID string, p jobs.Progress) error {
	id, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("jobs: invalid job id: %w", err)
	}

	// Get current tags to swap pending → running.
	currentTags, err := s.q.GetJobTags(ctx, id)
	if err != nil {
		return fmt.Errorf("jobs: get tags: %w", err)
	}
	tags := jobs.RebuildTags(currentTags, jobs.StatusRunning)

	return s.q.ReportProgress(ctx, dbgen.ReportProgressParams{
		ID:              id,
		ProgressStep:    p.Step,
		ProgressCurrent: int32(p.Current),
		ProgressTotal:   int32(p.Total),
		ProgressMessage: p.Message,
		Tags:            tags,
	})
}

func (s *Store) AddTags(ctx context.Context, jobID string, tags []string) error {
	id, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("jobs: invalid job id: %w", err)
	}
	return s.q.AddJobTags(ctx, dbgen.AddJobTagsParams{
		ID:      id,
		NewTags: tags,
	})
}

func (s *Store) CreateStep(ctx context.Context, jobID string, params jobs.RegisterStepParams) (*jobs.Step, error) {
	jid, err := uuid.Parse(jobID)
	if err != nil {
		return nil, fmt.Errorf("jobs: invalid job id: %w", err)
	}
	row, err := s.q.StartStep(ctx, dbgen.StartStepParams{
		JobID:    jid,
		Name:     params.Name,
		Sequence: int32(params.Sequence),
		Input:    params.Input,
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: start step: %w", err)
	}
	return rowToStep(row), nil
}

func (s *Store) CompleteStep(ctx context.Context, stepID string, params jobs.CompleteStepParams) error {
	id, err := uuid.Parse(stepID)
	if err != nil {
		return fmt.Errorf("jobs: invalid step id: %w", err)
	}
	return s.q.CompleteStep(ctx, dbgen.CompleteStepParams{
		ID:     id,
		Output: params.Output,
	})
}

func (s *Store) FailStep(ctx context.Context, stepID string, errMsg string) error {
	id, err := uuid.Parse(stepID)
	if err != nil {
		return fmt.Errorf("jobs: invalid step id: %w", err)
	}
	return s.q.FailStep(ctx, dbgen.FailStepParams{
		ID:    id,
		Error: errMsg,
	})
}

func (s *Store) GetJob(ctx context.Context, jobID string) (*jobs.Job, error) {
	id, err := uuid.Parse(jobID)
	if err != nil {
		return nil, fmt.Errorf("jobs: invalid job id: %w", err)
	}
	row, err := s.q.GetJob(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, jobs.ErrNotFound
		}
		return nil, fmt.Errorf("jobs: get: %w", err)
	}
	return rowToJob(row), nil
}

func (s *Store) GetJobByExternalReference(ctx context.Context, externalReference string) (*jobs.Job, error) {
	row, err := s.q.GetJobByExternalReference(ctx, externalReference)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: external_reference %s", jobs.ErrNotFound, externalReference)
		}
		return nil, fmt.Errorf("jobs: get by external_reference: %w", err)
	}
	return rowToJob(row), nil
}

func (s *Store) ListJobs(ctx context.Context, filter jobs.ListFilter) ([]jobs.Job, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = jobs.DefaultListLimit
	}

	var tags []string
	if len(filter.Tags) > 0 {
		tags = filter.Tags
	}

	rows, err := s.q.ListJobs(ctx, dbgen.ListJobsParams{
		Tags:         tags,
		ResultLimit:  int32(limit),
		ResultOffset: int32(filter.Offset),
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: list: %w", err)
	}

	result := make([]jobs.Job, len(rows))
	for i, row := range rows {
		result[i] = *rowToJob(row)
	}
	return result, nil
}

func (s *Store) GetSteps(ctx context.Context, jobID string) ([]jobs.Step, error) {
	jid, err := uuid.Parse(jobID)
	if err != nil {
		return nil, fmt.Errorf("jobs: invalid job id: %w", err)
	}
	rows, err := s.q.GetStepsByJobID(ctx, jid)
	if err != nil {
		return nil, fmt.Errorf("jobs: get steps: %w", err)
	}

	result := make([]jobs.Step, len(rows))
	for i, row := range rows {
		result[i] = *rowToStep(row)
	}
	return result, nil
}

// --- Row conversions ---

func rowToJob(row dbgen.Job) *jobs.Job {
	j := &jobs.Job{
		ID:                row.ID.String(),
		ExternalReference: row.ExternalReference,
		JobType:           row.JobType,
		Status:            jobs.Status(row.Status),
		Error:             row.Error,
		Input:             row.Input,
		Output:            row.Output,
		Metadata:          row.Metadata,
		Tags:              row.Tags,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
	if row.ProgressUpdatedAt.Valid {
		j.Progress = &jobs.Progress{
			Step:      row.ProgressStep,
			Current:   int(row.ProgressCurrent),
			Total:     int(row.ProgressTotal),
			Message:   row.ProgressMessage,
			UpdatedAt: row.ProgressUpdatedAt.Time,
		}
	}
	return j
}

func rowToStep(row dbgen.JobStep) *jobs.Step {
	s := &jobs.Step{
		ID:        row.ID.String(),
		JobID:     row.JobID.String(),
		Name:      row.Name,
		Sequence:  int(row.Sequence),
		Status:    jobs.Status(row.Status),
		Error:     row.Error,
		Input:     row.Input,
		Output:    row.Output,
		StartedAt: row.StartedAt,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.CompletedAt.Valid {
		s.CompletedAt = row.CompletedAt.Time
	}
	return s
}
