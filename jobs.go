// Package jobs provides a high-level SDK for durable job tracking built on EntityStore.
//
// Jobs are stored as EntityStore entities (type "jobs.v1.Job") with relationships to
// owners, teams, inboxes, and input/output documents. The entity schema is defined in
// proto/jobs/v1/job.proto with EntityStore matching annotations.
//
// For DBOS workflow integration, see the dbosutil subpackage.
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
	"github.com/laenen-partners/entitystore/gen/entitystore/v1/entitystorev1connect"
	jobsv1 "github.com/laenen-partners/jobs/gen/jobs/v1"
)

// EntityType is the proto-qualified entity type for jobs in EntityStore.
const EntityType = "jobs.v1.Job"

// Size limits for data fields.
const (
	MaxInputSize    = 1 << 20  // 1 MiB
	MaxOutputSize   = 1 << 20  // 1 MiB
	MaxMetadataSize = 64 << 10 // 64 KiB
)

// Status constants for job lifecycle.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// statusToProto maps SDK status strings to proto enum values.
var statusToProto = map[string]jobsv1.JobStatus{
	StatusPending:   jobsv1.JobStatus_JOB_STATUS_PENDING,
	StatusRunning:   jobsv1.JobStatus_JOB_STATUS_RUNNING,
	StatusCompleted: jobsv1.JobStatus_JOB_STATUS_COMPLETED,
	StatusFailed:    jobsv1.JobStatus_JOB_STATUS_FAILED,
	StatusCancelled: jobsv1.JobStatus_JOB_STATUS_CANCELLED,
}

// protoToStatus maps proto enum values to SDK status strings.
var protoToStatus = map[jobsv1.JobStatus]string{
	jobsv1.JobStatus_JOB_STATUS_PENDING:   StatusPending,
	jobsv1.JobStatus_JOB_STATUS_RUNNING:   StatusRunning,
	jobsv1.JobStatus_JOB_STATUS_COMPLETED: StatusCompleted,
	jobsv1.JobStatus_JOB_STATUS_FAILED:    StatusFailed,
	jobsv1.JobStatus_JOB_STATUS_CANCELLED: StatusCancelled,
}

// terminalStatuses are statuses that cannot transition to another state.
var terminalStatuses = map[string]bool{
	StatusCompleted: true,
	StatusFailed:    true,
	StatusCancelled: true,
}

// Relation type constants.
const (
	RelOwnedBy     = "owned_by"
	RelAssignedTo  = "assigned_to"
	RelCreatedFrom = "created_from"
	RelProcesses   = "processes"
	RelProduces    = "produces"
	RelStepOf      = "step_of"
)

// Client provides high-level job operations over EntityStore.
type Client struct {
	entities entitystorev1connect.EntityStoreServiceClient
}

// NewClient creates a Jobs SDK client backed by the given EntityStore client.
func NewClient(entities entitystorev1connect.EntityStoreServiceClient) *Client {
	return &Client{entities: entities}
}

// Job is the hydrated view of a job entity with its relationships resolved.
type Job struct {
	ID         string          `json:"id"`
	WorkflowID string          `json:"workflow_id"`
	JobType    string          `json:"job_type"`
	Status     string          `json:"status"`
	Progress   *Progress       `json:"progress,omitempty"`
	Error      string          `json:"error,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Output     json.RawMessage `json:"output,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	Tags       []string        `json:"tags,omitempty"`

	// Relationships (hydrated from EntityStore relations).
	Owner   *EntityRef  `json:"owner,omitempty"`
	Team    *EntityRef  `json:"team,omitempty"`
	Inbox   *EntityRef  `json:"inbox,omitempty"`
	Inputs  []EntityRef `json:"inputs,omitempty"`
	Outputs []EntityRef `json:"outputs,omitempty"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// EntityRef is a lightweight reference to a related entity.
type EntityRef struct {
	ID         string `json:"id"`
	EntityType string `json:"entity_type"`
}

// Progress tracks in-flight job progress.
type Progress struct {
	Step      string    `json:"step"`
	Current   int       `json:"current"`
	Total     int       `json:"total"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PublishParams are the inputs for creating a new job.
type PublishParams struct {
	WorkflowID string          `json:"workflow_id"`
	JobType    string          `json:"job_type"`
	OwnerID    string          `json:"owner_id"`          // required
	TeamID     string          `json:"team_id,omitempty"` // optional
	InboxID    string          `json:"inbox_id,omitempty"`
	InputIDs   []string        `json:"input_ids,omitempty"`
	Tags       []string        `json:"tags,omitempty"`     // additional caller-defined tags
	Input      json.RawMessage `json:"input,omitempty"`    // primary input payload (max 1 MiB)
	Metadata   json.RawMessage `json:"metadata,omitempty"` // ancillary caller metadata (max 64 KiB)
}

// FinalizeParams describe the final state of a completed/failed/cancelled job.
type FinalizeParams struct {
	Status    string          `json:"status"` // completed, failed, cancelled
	Error     string          `json:"error,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"` // primary output payload (max 1 MiB)
	OutputIDs []string        `json:"output_ids,omitempty"`
}

var marshaler = protojson.MarshalOptions{UseProtoNames: true}

// marshalJobProto serializes a jobs.v1.Job proto message to JSON bytes for EntityStore.
func marshalJobProto(pb *jobsv1.Job) ([]byte, error) {
	b, err := marshaler.Marshal(pb)
	if err != nil {
		return nil, fmt.Errorf("jobs: marshal proto: %w", err)
	}
	return b, nil
}

// unmarshalJobProto deserializes EntityStore data bytes into a jobs.v1.Job proto message.
func unmarshalJobProto(data []byte) (*jobsv1.Job, error) {
	pb := &jobsv1.Job{}
	if err := protojson.Unmarshal(data, pb); err != nil {
		return nil, fmt.Errorf("jobs: unmarshal proto: %w", err)
	}
	return pb, nil
}

// validateStatus checks that a status string is a known lifecycle state.
func validateStatus(status string) error {
	if _, ok := statusToProto[status]; !ok {
		return fmt.Errorf("jobs: invalid status %q: must be one of pending, running, completed, failed, cancelled", status)
	}
	return nil
}

// Publish creates a new job entity in EntityStore and wires up relationships.
func (c *Client) Publish(ctx context.Context, params PublishParams) (*Job, error) {
	if params.OwnerID == "" {
		return nil, fmt.Errorf("jobs: owner_id is required")
	}
	if len(params.Input) > MaxInputSize {
		return nil, fmt.Errorf("jobs: input exceeds maximum size of %d bytes", MaxInputSize)
	}
	if len(params.Metadata) > MaxMetadataSize {
		return nil, fmt.Errorf("jobs: metadata exceeds maximum size of %d bytes", MaxMetadataSize)
	}

	pb := &jobsv1.Job{
		WorkflowId: params.WorkflowID,
		JobType:    params.JobType,
		Status:     jobsv1.JobStatus_JOB_STATUS_PENDING,
		Input:      params.Input,
		Metadata:   params.Metadata,
	}
	dataBytes, err := marshalJobProto(pb)
	if err != nil {
		return nil, err
	}

	// Build tags: status tag + caller-provided tags + input ID tags.
	tags := append([]string{StatusPending}, params.Tags...)
	for _, id := range params.InputIDs {
		tags = append(tags, "input:"+id)
	}

	// Build BatchWrite operations: create entity + wire relationships atomically.
	ops := []*entitystorev1.BatchWriteOp{
		{Operation: &entitystorev1.BatchWriteOp_WriteEntity{
			WriteEntity: &entitystorev1.WriteEntityOp{
				Action:          entitystorev1.WriteAction_WRITE_ACTION_CREATE,
				EntityType:      EntityType,
				Data:            dataBytes,
				Tags:            tags,
				SourceUrn:       "system:jobs",
				ModelId:         "jobs",
				Fields:          []string{"workflow_id", "job_type", "status", "input", "metadata"},
				MatchMethod:     "direct",
				MatchConfidence: 1.0,
			},
		}},
	}

	resp, err := c.entities.BatchWrite(ctx, connect.NewRequest(&entitystorev1.BatchWriteRequest{
		Operations: ops,
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs: create entity: %w", err)
	}

	entity := resp.Msg.Results[0].GetEntity()
	jobID := entity.Id
	slog.InfoContext(ctx, "jobs: published",
		"job_id", jobID,
		"workflow_id", params.WorkflowID,
		"type", params.JobType,
		"owner_id", params.OwnerID,
	)

	// Wire relationships.
	if err := c.upsertRelation(ctx, jobID, params.OwnerID, RelOwnedBy); err != nil {
		return nil, fmt.Errorf("jobs: relate owner: %w", err)
	}

	if params.TeamID != "" {
		if err := c.upsertRelation(ctx, jobID, params.TeamID, RelAssignedTo); err != nil {
			return nil, fmt.Errorf("jobs: relate team: %w", err)
		}
	}

	if params.InboxID != "" {
		if err := c.upsertRelation(ctx, jobID, params.InboxID, RelCreatedFrom); err != nil {
			return nil, fmt.Errorf("jobs: relate inbox: %w", err)
		}
	}

	for _, inputID := range params.InputIDs {
		if err := c.upsertRelation(ctx, jobID, inputID, RelProcesses); err != nil {
			return nil, fmt.Errorf("jobs: relate input %s: %w", inputID, err)
		}
	}

	return &Job{
		ID:         jobID,
		WorkflowID: params.WorkflowID,
		JobType:    params.JobType,
		Status:     StatusPending,
		Input:      params.Input,
		Metadata:   params.Metadata,
		Tags:       tags,
		CreatedAt:  entity.CreatedAt,
		UpdatedAt:  entity.UpdatedAt,
	}, nil
}

// Finalize updates a job's final state (completed/failed/cancelled) and creates output relations.
func (c *Client) Finalize(ctx context.Context, jobID string, params FinalizeParams) error {
	if err := validateStatus(params.Status); err != nil {
		return err
	}
	if !terminalStatuses[params.Status] {
		return fmt.Errorf("jobs: finalize requires a terminal status (completed, failed, cancelled), got %q", params.Status)
	}
	if len(params.Output) > MaxOutputSize {
		return fmt.Errorf("jobs: output exceeds maximum size of %d bytes", MaxOutputSize)
	}

	// Guard against re-finalizing a job that is already in a terminal state.
	current, err := c.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("jobs: check current state: %w", err)
	}
	if terminalStatuses[current.Status] {
		return fmt.Errorf("%w: job %s is already %s", ErrAlreadyFinalized, jobID, current.Status)
	}

	pb := &jobsv1.Job{
		Status: statusToProto[params.Status],
		Error:  params.Error,
		Output: params.Output,
	}
	dataBytes, err := marshalJobProto(pb)
	if err != nil {
		return err
	}

	// Update entity data.
	_, err = c.entities.BatchWrite(ctx, connect.NewRequest(&entitystorev1.BatchWriteRequest{
		Operations: []*entitystorev1.BatchWriteOp{
			{Operation: &entitystorev1.BatchWriteOp_WriteEntity{
				WriteEntity: &entitystorev1.WriteEntityOp{
					Action:          entitystorev1.WriteAction_WRITE_ACTION_MERGE,
					MatchedEntityId: jobID,
					EntityType:      EntityType,
					Data:            dataBytes,
					SourceUrn:       "system:jobs",
					ModelId:         "jobs",
					Fields:          []string{"status", "error", "output"},
					MatchMethod:     "direct",
					MatchConfidence: 1.0,
				},
			}},
		},
	}))
	if err != nil {
		return fmt.Errorf("jobs: update entity: %w", err)
	}

	// Replace status tags: set final status, remove transient ones, add output ID tags.
	finalTags := rebuildTags(current.Tags, params.Status)
	for _, id := range params.OutputIDs {
		finalTags = append(finalTags, "output:"+id)
	}
	if _, err := c.entities.SetTags(ctx, connect.NewRequest(&entitystorev1.SetTagsRequest{
		EntityId: jobID,
		Tags:     finalTags,
	})); err != nil {
		return fmt.Errorf("jobs: set final tags: %w", err)
	}

	// Create output relations.
	for _, outputID := range params.OutputIDs {
		if err := c.upsertRelation(ctx, jobID, outputID, RelProduces); err != nil {
			return fmt.Errorf("jobs: relate output %s: %w", outputID, err)
		}
	}

	slog.InfoContext(ctx, "jobs: finalized",
		"job_id", jobID,
		"status", params.Status,
		"previous_status", current.Status,
	)
	return nil
}

// ReportProgress updates the job's progress field and sets the running tag.
func (c *Client) ReportProgress(ctx context.Context, jobID string, p Progress) error {
	pb := &jobsv1.Job{
		Status: jobsv1.JobStatus_JOB_STATUS_RUNNING,
		Progress: &jobsv1.Progress{
			Step:      p.Step,
			Current:   int32(p.Current),
			Total:     int32(p.Total),
			Message:   p.Message,
			UpdatedAt: timestamppb.Now(),
		},
	}
	dataBytes, err := marshalJobProto(pb)
	if err != nil {
		return err
	}

	_, err = c.entities.BatchWrite(ctx, connect.NewRequest(&entitystorev1.BatchWriteRequest{
		Operations: []*entitystorev1.BatchWriteOp{
			{Operation: &entitystorev1.BatchWriteOp_WriteEntity{
				WriteEntity: &entitystorev1.WriteEntityOp{
					Action:          entitystorev1.WriteAction_WRITE_ACTION_MERGE,
					MatchedEntityId: jobID,
					EntityType:      EntityType,
					Data:            dataBytes,
					SourceUrn:       "system:jobs",
					ModelId:         "jobs",
					Fields:          []string{"status", "progress"},
					MatchMethod:     "direct",
					MatchConfidence: 1.0,
				},
			}},
		},
	}))
	if err != nil {
		return fmt.Errorf("jobs: update progress: %w", err)
	}

	// Swap pending → running tag on first progress report.
	if _, err := c.entities.RemoveTag(ctx, connect.NewRequest(&entitystorev1.RemoveTagRequest{
		EntityId: jobID,
		Tag:      StatusPending,
	})); err != nil {
		return fmt.Errorf("jobs: remove pending tag: %w", err)
	}
	if _, err := c.entities.AddTags(ctx, connect.NewRequest(&entitystorev1.AddTagsRequest{
		EntityId: jobID,
		Tags:     []string{StatusRunning},
	})); err != nil {
		return fmt.Errorf("jobs: set running tag: %w", err)
	}

	return nil
}

// Get retrieves a job by ID, hydrating relationships from EntityStore.
func (c *Client) Get(ctx context.Context, jobID string) (*Job, error) {
	resp, err := c.entities.GetEntity(ctx, connect.NewRequest(&entitystorev1.GetEntityRequest{
		Id: jobID,
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs: get entity: %w", err)
	}

	job, err := entityToJob(resp.Msg.Entity)
	if err != nil {
		return nil, err
	}

	// Hydrate relationships.
	relResp, err := c.entities.GetRelationsFromEntity(ctx, connect.NewRequest(&entitystorev1.GetRelationsFromEntityRequest{
		EntityId: jobID,
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs: get relations: %w", err)
	}

	for _, rel := range relResp.Msg.Relations {
		ref := EntityRef{ID: rel.TargetId}
		switch rel.RelationType {
		case RelOwnedBy:
			job.Owner = &ref
		case RelAssignedTo:
			job.Team = &ref
		case RelCreatedFrom:
			job.Inbox = &ref
		case RelProcesses:
			job.Inputs = append(job.Inputs, ref)
		case RelProduces:
			job.Outputs = append(job.Outputs, ref)
		}
	}

	return job, nil
}

// GetByWorkflowID retrieves a job by its workflow_id anchor.
// Returns ErrNotFound if no job exists for the given workflow ID.
func (c *Client) GetByWorkflowID(ctx context.Context, workflowID string) (*Job, error) {
	resp, err := c.entities.FindByAnchors(ctx, connect.NewRequest(&entitystorev1.FindByAnchorsRequest{
		EntityType: EntityType,
		Anchors: []*entitystorev1.AnchorQuery{
			{Field: "workflow_id", Value: workflowID},
		},
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs: find by workflow_id: %w", err)
	}

	if len(resp.Msg.Entities) == 0 {
		return nil, fmt.Errorf("%w: workflow_id %s", ErrNotFound, workflowID)
	}

	job, err := entityToJob(resp.Msg.Entities[0])
	if err != nil {
		return nil, err
	}

	return job, nil
}

// Cancel marks a job as cancelled.
func (c *Client) Cancel(ctx context.Context, jobID string) error {
	return c.Finalize(ctx, jobID, FinalizeParams{
		Status: StatusCancelled,
	})
}

func (c *Client) upsertRelation(ctx context.Context, sourceID, targetID, relationType string) error {
	_, err := c.entities.BatchWrite(ctx, connect.NewRequest(&entitystorev1.BatchWriteRequest{
		Operations: []*entitystorev1.BatchWriteOp{
			{Operation: &entitystorev1.BatchWriteOp_UpsertRelation{
				UpsertRelation: &entitystorev1.UpsertRelationOp{
					SourceId:     sourceID,
					TargetId:     targetID,
					RelationType: relationType,
					SourceUrn:    "system:jobs",
				},
			}},
		},
	}))
	return err
}

// rebuildTags replaces any status tag in the existing tags with the new status.
func rebuildTags(existingTags []string, newStatus string) []string {
	tags := make([]string, 0, len(existingTags)+1)
	for _, t := range existingTags {
		if _, isStatus := statusToProto[t]; !isStatus {
			tags = append(tags, t)
		}
	}
	return append(tags, newStatus)
}

func entityToJob(e *entitystorev1.Entity) (*Job, error) {
	if e == nil {
		return nil, fmt.Errorf("jobs: entity is nil")
	}

	var pb *jobsv1.Job
	if len(e.Data) > 0 {
		var err error
		pb, err = unmarshalJobProto(e.Data)
		if err != nil {
			return nil, err
		}
	} else {
		pb = &jobsv1.Job{}
	}

	status := protoToStatus[pb.Status]

	var progress *Progress
	if pb.Progress != nil {
		progress = &Progress{
			Step:    pb.Progress.Step,
			Current: int(pb.Progress.Current),
			Total:   int(pb.Progress.Total),
			Message: pb.Progress.Message,
		}
		if pb.Progress.UpdatedAt != nil {
			progress.UpdatedAt = pb.Progress.UpdatedAt.AsTime()
		}
	}

	return &Job{
		ID:         e.Id,
		WorkflowID: pb.WorkflowId,
		JobType:    pb.JobType,
		Status:     status,
		Progress:   progress,
		Error:      pb.Error,
		Input:      pb.Input,
		Output:     pb.Output,
		Metadata:   pb.Metadata,
		Tags:       e.Tags,
		CreatedAt:  e.CreatedAt,
		UpdatedAt:  e.UpdatedAt,
	}, nil
}
