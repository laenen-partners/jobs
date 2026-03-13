package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// mockStore implements JobStore for unit testing with pure Go types.
type mockStore struct {
	jobs   map[string]*Job
	steps  map[string]*Step
	nextID int
}

func newMockStore() *mockStore {
	return &mockStore{
		jobs:  make(map[string]*Job),
		steps: make(map[string]*Step),
	}
}

func (m *mockStore) CreateJob(_ context.Context, params RegisterJobParams) (*Job, error) {
	m.nextID++
	tags := append([]string{string(StatusPending)}, params.Tags...)

	job := &Job{
		ID:                fmt.Sprintf("job-%d", m.nextID),
		ExternalReference: params.ExternalReference,
		JobType:           params.JobType,
		Status:            StatusPending,
		Input:             params.Input,
		Metadata:          params.Metadata,
		Tags:              tags,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	m.jobs[job.ID] = job
	return job, nil
}

func (m *mockStore) FinalizeJob(_ context.Context, jobID string, params FinalizeParams) error {
	job, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("jobs: get entity: not found")
	}
	if TerminalStatuses[job.Status] {
		return fmt.Errorf("%w: job %s is already %s", ErrAlreadyFinalized, jobID, job.Status)
	}
	job.Status = params.Status
	job.Error = params.Error
	job.Output = params.Output
	job.Tags = RebuildTags(job.Tags, params.Status)
	job.Tags = append(job.Tags, params.Tags...)
	job.UpdatedAt = time.Now()
	return nil
}

func (m *mockStore) ReportProgress(_ context.Context, jobID string, p Progress) error {
	job, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("jobs: not found: %s", jobID)
	}
	p.UpdatedAt = time.Now()
	job.Status = StatusRunning
	job.Progress = &p
	job.Tags = RebuildTags(job.Tags, StatusRunning)
	job.UpdatedAt = time.Now()
	return nil
}

func (m *mockStore) CreateStep(_ context.Context, jobID string, params RegisterStepParams) (*Step, error) {
	m.nextID++
	step := &Step{
		ID:        fmt.Sprintf("step-%d", m.nextID),
		JobID:     jobID,
		Name:      params.Name,
		Sequence:  params.Sequence,
		Status:    StatusRunning,
		Input:     params.Input,
		StartedAt: time.Now(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.steps[step.ID] = step
	return step, nil
}

func (m *mockStore) CompleteStep(_ context.Context, stepID string, params CompleteStepParams) error {
	step, ok := m.steps[stepID]
	if !ok {
		return fmt.Errorf("jobs: step not found: %s", stepID)
	}
	step.Status = StatusCompleted
	step.Output = params.Output
	step.CompletedAt = time.Now()
	step.UpdatedAt = time.Now()
	return nil
}

func (m *mockStore) FailStep(_ context.Context, stepID string, stepErr string) error {
	step, ok := m.steps[stepID]
	if !ok {
		return fmt.Errorf("jobs: step not found: %s", stepID)
	}
	step.Status = StatusFailed
	step.Error = stepErr
	step.CompletedAt = time.Now()
	step.UpdatedAt = time.Now()
	return nil
}

func (m *mockStore) AddTags(_ context.Context, jobID string, tags []string) error {
	job, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("jobs: not found: %s", jobID)
	}
	job.Tags = append(job.Tags, tags...)
	job.UpdatedAt = time.Now()
	return nil
}

func (m *mockStore) GetJob(_ context.Context, jobID string) (*Job, error) {
	job, ok := m.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("jobs: not found: %s", jobID)
	}
	return job, nil
}

func (m *mockStore) GetJobByExternalReference(_ context.Context, externalReference string) (*Job, error) {
	for _, job := range m.jobs {
		if job.ExternalReference == externalReference {
			return job, nil
		}
	}
	return nil, fmt.Errorf("%w: external_reference %s", ErrNotFound, externalReference)
}

func (m *mockStore) ListJobs(_ context.Context, filter ListFilter) ([]Job, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}

	var result []Job
	skipped := 0
	for _, job := range m.jobs {
		if len(filter.Tags) > 0 && !HasAllTags(job.Tags, filter.Tags) {
			continue
		}
		if skipped < filter.Offset {
			skipped++
			continue
		}
		result = append(result, *job)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (m *mockStore) GetSteps(_ context.Context, jobID string) ([]Step, error) {
	var result []Step
	for _, step := range m.steps {
		if step.JobID == jobID {
			result = append(result, *step)
		}
	}
	return result, nil
}

// --- Tests ---

func TestRegisterJob_Success(t *testing.T) {
	client := NewClient(newMockStore())
	ctx := context.Background()

	job, err := client.RegisterJob(ctx, RegisterJobParams{
		ExternalReference: "wf-1",
		JobType:           "file_processing",
		Tags:              []string{"owner:user-1", "team:team-1", "input:doc-1", "input:doc-2"},
	})
	if err != nil {
		t.Fatalf("RegisterJob: %v", err)
	}

	if job.ID == "" {
		t.Error("expected non-empty job ID")
	}
	if job.Status != StatusPending {
		t.Errorf("status = %q, want %q", job.Status, StatusPending)
	}
	if job.ExternalReference != "wf-1" {
		t.Errorf("external_reference = %q, want %q", job.ExternalReference, "wf-1")
	}
	if job.JobType != "file_processing" {
		t.Errorf("job_type = %q, want %q", job.JobType, "file_processing")
	}
}

func TestRegisterJob_InputTooLarge(t *testing.T) {
	client := NewClient(newMockStore())
	bigInput := json.RawMessage(strings.Repeat("x", MaxInputSize+1))
	_, err := client.RegisterJob(context.Background(), RegisterJobParams{
		Input: bigInput,
	})
	if err == nil {
		t.Fatal("expected error for oversized input")
	}
	if !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("error = %q, want mention of maximum size", err)
	}
}

func TestRegisterJob_MetadataTooLarge(t *testing.T) {
	client := NewClient(newMockStore())
	bigMeta := json.RawMessage(strings.Repeat("x", MaxMetadataSize+1))
	_, err := client.RegisterJob(context.Background(), RegisterJobParams{
		Metadata: bigMeta,
	})
	if err == nil {
		t.Fatal("expected error for oversized metadata")
	}
	if !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("error = %q, want mention of maximum size", err)
	}
}

func TestFinalizeJob_Success(t *testing.T) {
	client := NewClient(newMockStore())
	ctx := context.Background()

	job, err := client.RegisterJob(ctx, RegisterJobParams{
		ExternalReference: "wf-1",
		JobType:           "test",
	})
	if err != nil {
		t.Fatalf("RegisterJob: %v", err)
	}

	output := json.RawMessage(`{"result":"ok"}`)
	err = client.FinalizeJob(ctx, job.ID, FinalizeParams{
		Status: StatusCompleted,
		Output: output,
		Tags:   []string{"output:out-1"},
	})
	if err != nil {
		t.Fatalf("FinalizeJob: %v", err)
	}

	got, err := client.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob after finalize: %v", err)
	}
	if got.Status != StatusCompleted {
		t.Errorf("status = %q, want %q", got.Status, StatusCompleted)
	}
	if !HasAllTags(got.Tags, []string{"output:out-1"}) {
		t.Errorf("tags = %v, want output:out-1", got.Tags)
	}
}

func TestFinalizeJob_AlreadyFinalized(t *testing.T) {
	client := NewClient(newMockStore())
	ctx := context.Background()

	job, err := client.RegisterJob(ctx, RegisterJobParams{
		ExternalReference: "wf-1",
		JobType:           "test",
	})
	if err != nil {
		t.Fatalf("RegisterJob: %v", err)
	}

	err = client.FinalizeJob(ctx, job.ID, FinalizeParams{Status: StatusCompleted})
	if err != nil {
		t.Fatalf("first FinalizeJob: %v", err)
	}

	err = client.FinalizeJob(ctx, job.ID, FinalizeParams{Status: StatusFailed})
	if err == nil {
		t.Fatal("expected ErrAlreadyFinalized")
	}
	if !strings.Contains(err.Error(), "already finalized") {
		t.Errorf("error = %q, want mention of already finalized", err)
	}
}

func TestFinalizeJob_InvalidStatus(t *testing.T) {
	client := NewClient(newMockStore())
	err := client.FinalizeJob(context.Background(), "ent-1", FinalizeParams{
		Status: "banana",
	})
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
	if !strings.Contains(err.Error(), "invalid status") {
		t.Errorf("error = %q, want mention of invalid status", err)
	}
}

func TestFinalizeJob_NonTerminalStatus(t *testing.T) {
	client := NewClient(newMockStore())
	err := client.FinalizeJob(context.Background(), "ent-1", FinalizeParams{
		Status: StatusRunning,
	})
	if err == nil {
		t.Fatal("expected error for non-terminal status")
	}
	if !strings.Contains(err.Error(), "terminal status") {
		t.Errorf("error = %q, want mention of terminal status", err)
	}
}

func TestFinalizeJob_OutputTooLarge(t *testing.T) {
	client := NewClient(newMockStore())
	big := json.RawMessage(strings.Repeat("x", MaxOutputSize+1))
	err := client.FinalizeJob(context.Background(), "ent-1", FinalizeParams{
		Status: StatusCompleted,
		Output: big,
	})
	if err == nil {
		t.Fatal("expected error for oversized output")
	}
	if !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("error = %q, want mention of maximum size", err)
	}
}

func TestGetJob_ReturnsTags(t *testing.T) {
	client := NewClient(newMockStore())
	ctx := context.Background()

	job, err := client.RegisterJob(ctx, RegisterJobParams{
		ExternalReference: "wf-1",
		JobType:           "test",
		Tags:              []string{"owner:user-1", "team:team-1", "input:doc-1"},
	})
	if err != nil {
		t.Fatalf("RegisterJob: %v", err)
	}

	got, err := client.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if !HasAllTags(got.Tags, []string{"owner:user-1", "team:team-1", "input:doc-1"}) {
		t.Errorf("tags = %v, want owner:user-1, team:team-1, input:doc-1", got.Tags)
	}
}

func TestGetByExternalReference_Success(t *testing.T) {
	client := NewClient(newMockStore())
	ctx := context.Background()

	registered, err := client.RegisterJob(ctx, RegisterJobParams{
		ExternalReference: "wf-42",
		JobType:           "test",
	})
	if err != nil {
		t.Fatalf("RegisterJob: %v", err)
	}

	got, err := client.GetByExternalReference(ctx, "wf-42")
	if err != nil {
		t.Fatalf("GetByExternalReference: %v", err)
	}

	if got.ID != registered.ID {
		t.Errorf("ID = %q, want %q", got.ID, registered.ID)
	}
	if got.ExternalReference != "wf-42" {
		t.Errorf("external_reference = %q, want wf-42", got.ExternalReference)
	}
}

func TestGetByExternalReference_NotFound(t *testing.T) {
	client := NewClient(newMockStore())
	_, err := client.GetByExternalReference(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want mention of not found", err)
	}
}

func TestCancelJob(t *testing.T) {
	client := NewClient(newMockStore())
	ctx := context.Background()

	job, err := client.RegisterJob(ctx, RegisterJobParams{
		ExternalReference: "wf-1",
		JobType:           "test",
	})
	if err != nil {
		t.Fatalf("RegisterJob: %v", err)
	}

	err = client.CancelJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	got, err := client.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob after cancel: %v", err)
	}
	if got.Status != StatusCancelled {
		t.Errorf("status = %q, want %q", got.Status, StatusCancelled)
	}
}

func TestReportProgress(t *testing.T) {
	client := NewClient(newMockStore())
	ctx := context.Background()

	job, err := client.RegisterJob(ctx, RegisterJobParams{
		ExternalReference: "wf-1",
		JobType:           "test",
	})
	if err != nil {
		t.Fatalf("RegisterJob: %v", err)
	}

	err = client.ReportProgress(ctx, job.ID, Progress{
		Step:    "converting",
		Current: 2,
		Total:   5,
		Message: "processing page 2",
	})
	if err != nil {
		t.Fatalf("ReportProgress: %v", err)
	}

	got, err := client.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != StatusRunning {
		t.Errorf("status = %q, want %q", got.Status, StatusRunning)
	}
	if got.Progress == nil {
		t.Fatal("progress is nil")
	}
	if got.Progress.Step != "converting" {
		t.Errorf("progress.step = %q, want converting", got.Progress.Step)
	}
	if got.Progress.Current != 2 || got.Progress.Total != 5 {
		t.Errorf("progress = %d/%d, want 2/5", got.Progress.Current, got.Progress.Total)
	}
	if got.Progress.UpdatedAt.IsZero() {
		t.Error("progress.updated_at should be set")
	}
}

func TestListJobs_WithTagFilters(t *testing.T) {
	client := NewClient(newMockStore())
	ctx := context.Background()

	_, err := client.RegisterJob(ctx, RegisterJobParams{
		ExternalReference: "wf-a",
		JobType:           "type_a",
		Tags:              []string{"type:type_a"},
	})
	if err != nil {
		t.Fatalf("RegisterJob: %v", err)
	}
	_, err = client.RegisterJob(ctx, RegisterJobParams{
		ExternalReference: "wf-b",
		JobType:           "type_b",
		Tags:              []string{"type:type_b"},
	})
	if err != nil {
		t.Fatalf("RegisterJob: %v", err)
	}

	all, err := client.ListJobs(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("ListJobs all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("len = %d, want 2", len(all))
	}

	filtered, err := client.ListJobs(ctx, ListFilter{Tags: []string{"type:type_a"}})
	if err != nil {
		t.Fatalf("ListJobs filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("len = %d, want 1", len(filtered))
	}
}

func TestListJobs_Pagination(t *testing.T) {
	client := NewClient(newMockStore())
	ctx := context.Background()

	for i := range 5 {
		_, err := client.RegisterJob(ctx, RegisterJobParams{
			ExternalReference: fmt.Sprintf("wf-%d", i),
			JobType:           "test",
		})
		if err != nil {
			t.Fatalf("RegisterJob: %v", err)
		}
	}

	page, err := client.ListJobs(ctx, ListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(page) != 2 {
		t.Errorf("len = %d, want 2", len(page))
	}
}

func TestRebuildTags(t *testing.T) {
	tags := RebuildTags([]string{"pending", "custom", "env:prod"}, StatusCompleted)

	hasCustom, hasEnv, hasCompleted, hasPending := false, false, false, false
	for _, tag := range tags {
		switch tag {
		case "custom":
			hasCustom = true
		case "env:prod":
			hasEnv = true
		case string(StatusCompleted):
			hasCompleted = true
		case string(StatusPending):
			hasPending = true
		}
	}

	if !hasCustom || !hasEnv {
		t.Error("non-status tags should be preserved")
	}
	if !hasCompleted {
		t.Error("new status tag should be added")
	}
	if hasPending {
		t.Error("old status tag should be removed")
	}
}

func TestValidateStatus(t *testing.T) {
	for _, s := range []Status{StatusPending, StatusRunning, StatusCompleted, StatusFailed, StatusCancelled} {
		if err := ValidateStatus(s); err != nil {
			t.Errorf("ValidateStatus(%q) = %v, want nil", s, err)
		}
	}
	if err := ValidateStatus("banana"); err == nil {
		t.Error("ValidateStatus(banana) = nil, want error")
	}
}

func TestNoopClient_TrackRunExecutesWork(t *testing.T) {
	client := NoopClient()
	called := false
	err := client.TrackRun(context.Background(), RunParams{}, func(ctx context.Context, rc *RunContext) error {
		called = true
		// Progress should be safe to call on noop
		rc.Progress(ctx, Progress{Step: "test", Current: 1, Total: 1})
		return nil
	})
	if err != nil {
		t.Fatalf("TrackRun: %v", err)
	}
	if !called {
		t.Error("work function was not called")
	}
}

func TestNoopClient_DirectOpsReturnError(t *testing.T) {
	client := NoopClient()
	ctx := context.Background()

	if _, err := client.GetJob(ctx, "id"); err == nil {
		t.Error("expected ErrNoStore from GetJob")
	}
	if _, err := client.GetByExternalReference(ctx, "wf"); err == nil {
		t.Error("expected ErrNoStore from GetByExternalReference")
	}
	if _, err := client.ListJobs(ctx, ListFilter{}); err == nil {
		t.Error("expected ErrNoStore from ListJobs")
	}
}
