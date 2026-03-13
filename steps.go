package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	entitystorev1 "github.com/laenen-partners/entitystore/gen/entitystore/v1"
	jobsv1 "github.com/laenen-partners/jobs/gen/jobs/v1"
)

// StepEntityType is the proto-qualified entity type for job steps in EntityStore.
const StepEntityType = "jobs.v1.JobStep"

// Size limits for step data fields.
const (
	MaxStepInputSize  = 256 << 10 // 256 KiB
	MaxStepOutputSize = 256 << 10 // 256 KiB
)

// Step is the hydrated view of a job step entity.
type Step struct {
	ID          string          `json:"id"`
	JobID       string          `json:"job_id"`
	Name        string          `json:"name"`
	Sequence    int             `json:"sequence"`
	Status      string          `json:"status"`
	Error       string          `json:"error,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
	Output      json.RawMessage `json:"output,omitempty"`
	StartedAt   time.Time       `json:"started_at,omitzero"`
	CompletedAt time.Time       `json:"completed_at,omitzero"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

// StartStepParams are the inputs for creating a new step within a job.
type StartStepParams struct {
	Name     string          `json:"name"`
	Sequence int             `json:"sequence"`
	Input    json.RawMessage `json:"input,omitempty"` // max 256 KiB
}

// CompleteStepParams describe the outcome of a finished step.
type CompleteStepParams struct {
	Output json.RawMessage `json:"output,omitempty"` // max 256 KiB
}

// StartStep creates a new step entity linked to a parent job via a step_of relation.
// The step is created in running status with a started_at timestamp.
func (s *Store) StartStep(ctx context.Context, jobID string, params StartStepParams) (*Step, error) {
	if params.Name == "" {
		return nil, fmt.Errorf("jobs: step name is required")
	}
	if len(params.Input) > MaxStepInputSize {
		return nil, fmt.Errorf("jobs: step input exceeds maximum size of %d bytes", MaxStepInputSize)
	}

	now := timestamppb.Now()
	pb := &jobsv1.JobStep{
		Name:      params.Name,
		Sequence:  int32(params.Sequence),
		Status:    jobsv1.JobStatus_JOB_STATUS_RUNNING,
		Input:     params.Input,
		StartedAt: now,
	}
	dataBytes, err := marshalStepProto(pb)
	if err != nil {
		return nil, err
	}

	resp, err := s.entities.BatchWrite(ctx, connect.NewRequest(&entitystorev1.BatchWriteRequest{
		Operations: []*entitystorev1.BatchWriteOp{
			{Operation: &entitystorev1.BatchWriteOp_WriteEntity{
				WriteEntity: &entitystorev1.WriteEntityOp{
					Action:          entitystorev1.WriteAction_WRITE_ACTION_CREATE,
					EntityType:      StepEntityType,
					Data:            dataBytes,
					Tags:            []string{StatusRunning},
					SourceUrn:       "system:jobs",
					ModelId:         "jobs",
					Fields:          []string{"name", "sequence", "status", "input", "started_at"},
					MatchMethod:     "direct",
					MatchConfidence: 1.0,
				},
			}},
		},
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs: create step entity: %w", err)
	}

	stepEntity := resp.Msg.Results[0].GetEntity()
	stepID := stepEntity.Id

	// Link step to parent job.
	if err := s.upsertRelation(ctx, stepID, jobID, RelStepOf); err != nil {
		return nil, fmt.Errorf("jobs: relate step to job: %w", err)
	}

	slog.InfoContext(ctx, "jobs: step started",
		"step_id", stepID,
		"job_id", jobID,
		"name", params.Name,
		"sequence", params.Sequence,
	)

	return &Step{
		ID:        stepID,
		JobID:     jobID,
		Name:      params.Name,
		Sequence:  params.Sequence,
		Status:    StatusRunning,
		Input:     params.Input,
		StartedAt: now.AsTime(),
		CreatedAt: stepEntity.CreatedAt,
		UpdatedAt: stepEntity.UpdatedAt,
	}, nil
}

// CompleteStep marks a step as completed with optional output data.
func (s *Store) CompleteStep(ctx context.Context, stepID string, params CompleteStepParams) error {
	if len(params.Output) > MaxStepOutputSize {
		return fmt.Errorf("jobs: step output exceeds maximum size of %d bytes", MaxStepOutputSize)
	}

	now := timestamppb.Now()
	pb := &jobsv1.JobStep{
		Status:      jobsv1.JobStatus_JOB_STATUS_COMPLETED,
		Output:      params.Output,
		CompletedAt: now,
	}
	dataBytes, err := marshalStepProto(pb)
	if err != nil {
		return err
	}

	_, err = s.entities.BatchWrite(ctx, connect.NewRequest(&entitystorev1.BatchWriteRequest{
		Operations: []*entitystorev1.BatchWriteOp{
			{Operation: &entitystorev1.BatchWriteOp_WriteEntity{
				WriteEntity: &entitystorev1.WriteEntityOp{
					Action:          entitystorev1.WriteAction_WRITE_ACTION_MERGE,
					MatchedEntityId: stepID,
					EntityType:      StepEntityType,
					Data:            dataBytes,
					SourceUrn:       "system:jobs",
					ModelId:         "jobs",
					Fields:          []string{"status", "output", "completed_at"},
					MatchMethod:     "direct",
					MatchConfidence: 1.0,
				},
			}},
		},
	}))
	if err != nil {
		return fmt.Errorf("jobs: update step entity: %w", err)
	}

	if _, err := s.entities.SetTags(ctx, connect.NewRequest(&entitystorev1.SetTagsRequest{
		EntityId: stepID,
		Tags:     []string{StatusCompleted},
	})); err != nil {
		return fmt.Errorf("jobs: set step completed tag: %w", err)
	}

	slog.InfoContext(ctx, "jobs: step completed", "step_id", stepID)
	return nil
}

// FailStep marks a step as failed with an error message.
func (s *Store) FailStep(ctx context.Context, stepID string, stepErr string) error {
	now := timestamppb.Now()
	pb := &jobsv1.JobStep{
		Status:      jobsv1.JobStatus_JOB_STATUS_FAILED,
		Error:       stepErr,
		CompletedAt: now,
	}
	dataBytes, err := marshalStepProto(pb)
	if err != nil {
		return err
	}

	_, err = s.entities.BatchWrite(ctx, connect.NewRequest(&entitystorev1.BatchWriteRequest{
		Operations: []*entitystorev1.BatchWriteOp{
			{Operation: &entitystorev1.BatchWriteOp_WriteEntity{
				WriteEntity: &entitystorev1.WriteEntityOp{
					Action:          entitystorev1.WriteAction_WRITE_ACTION_MERGE,
					MatchedEntityId: stepID,
					EntityType:      StepEntityType,
					Data:            dataBytes,
					SourceUrn:       "system:jobs",
					ModelId:         "jobs",
					Fields:          []string{"status", "error", "completed_at"},
					MatchMethod:     "direct",
					MatchConfidence: 1.0,
				},
			}},
		},
	}))
	if err != nil {
		return fmt.Errorf("jobs: update step entity: %w", err)
	}

	if _, err := s.entities.SetTags(ctx, connect.NewRequest(&entitystorev1.SetTagsRequest{
		EntityId: stepID,
		Tags:     []string{StatusFailed},
	})); err != nil {
		return fmt.Errorf("jobs: set step failed tag: %w", err)
	}

	slog.InfoContext(ctx, "jobs: step failed", "step_id", stepID, "error", stepErr)
	return nil
}

// GetSteps retrieves all steps linked to a job via step_of relations.
func (s *Store) GetSteps(ctx context.Context, jobID string) ([]Step, error) {
	resp, err := s.entities.FindConnectedByType(ctx, connect.NewRequest(&entitystorev1.FindConnectedByTypeRequest{
		EntityId:      jobID,
		EntityType:    StepEntityType,
		RelationTypes: []string{RelStepOf},
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs: find steps: %w", err)
	}

	steps := make([]Step, 0, len(resp.Msg.Entities))
	for _, e := range resp.Msg.Entities {
		step, err := entityToStep(e, jobID)
		if err != nil {
			continue // skip malformed step entities
		}
		steps = append(steps, *step)
	}

	return steps, nil
}

func marshalStepProto(pb *jobsv1.JobStep) ([]byte, error) {
	b, err := marshaler.Marshal(pb)
	if err != nil {
		return nil, fmt.Errorf("jobs: marshal step proto: %w", err)
	}
	return b, nil
}

func unmarshalStepProto(data []byte) (*jobsv1.JobStep, error) {
	pb := &jobsv1.JobStep{}
	if err := protojson.Unmarshal(data, pb); err != nil {
		return nil, fmt.Errorf("jobs: unmarshal step proto: %w", err)
	}
	return pb, nil
}

func entityToStep(e *entitystorev1.Entity, jobID string) (*Step, error) {
	if e == nil {
		return nil, fmt.Errorf("jobs: step entity is nil")
	}

	var pb *jobsv1.JobStep
	if len(e.Data) > 0 {
		var err error
		pb, err = unmarshalStepProto(e.Data)
		if err != nil {
			return nil, err
		}
	} else {
		pb = &jobsv1.JobStep{}
	}

	step := &Step{
		ID:        e.Id,
		JobID:     jobID,
		Name:      pb.Name,
		Sequence:  int(pb.Sequence),
		Status:    protoToStatus[pb.Status],
		Error:     pb.Error,
		Input:     pb.Input,
		Output:    pb.Output,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}

	if pb.StartedAt != nil {
		step.StartedAt = pb.StartedAt.AsTime()
	}
	if pb.CompletedAt != nil {
		step.CompletedAt = pb.CompletedAt.AsTime()
	}

	return step, nil
}
