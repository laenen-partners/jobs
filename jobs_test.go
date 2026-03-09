package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"

	entitystorev1 "github.com/laenen-partners/entitystore/gen/entitystore/v1"
	"github.com/laenen-partners/entitystore/gen/entitystore/v1/entitystorev1connect"
)

// mockEntityStore implements entitystorev1connect.EntityStoreServiceClient for testing.
type mockEntityStore struct {
	entitystorev1connect.UnimplementedEntityStoreServiceHandler

	entities  map[string]*entitystorev1.Entity
	relations []*entitystorev1.Relation
	nextID    int

	// hooks for assertions
	insertCalled     int
	updateCalled     int
	upsertRelCalled  int
	findAnchorCalled int
	setTagsCalled    int
}

func newMockStore() *mockEntityStore {
	return &mockEntityStore{
		entities: make(map[string]*entitystorev1.Entity),
	}
}

func (m *mockEntityStore) InsertEntity(_ context.Context, req *connect.Request[entitystorev1.InsertEntityRequest]) (*connect.Response[entitystorev1.InsertEntityResponse], error) {
	m.insertCalled++
	m.nextID++
	id := fmt.Sprintf("ent-%d", m.nextID)
	e := &entitystorev1.Entity{
		Id:         id,
		EntityType: req.Msg.EntityType,
		Data:       req.Msg.Data,
		Tags:       req.Msg.Tags,
		CreatedAt:  "2026-03-09T00:00:00Z",
		UpdatedAt:  "2026-03-09T00:00:00Z",
	}
	m.entities[id] = e
	return connect.NewResponse(&entitystorev1.InsertEntityResponse{Entity: e}), nil
}

func (m *mockEntityStore) UpdateEntity(_ context.Context, req *connect.Request[entitystorev1.UpdateEntityRequest]) (*connect.Response[entitystorev1.UpdateEntityResponse], error) {
	m.updateCalled++
	e, ok := m.entities[req.Msg.Id]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("entity not found"))
	}
	e.Data = req.Msg.Data
	return connect.NewResponse(&entitystorev1.UpdateEntityResponse{}), nil
}

func (m *mockEntityStore) GetEntity(_ context.Context, req *connect.Request[entitystorev1.GetEntityRequest]) (*connect.Response[entitystorev1.GetEntityResponse], error) {
	e, ok := m.entities[req.Msg.Id]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("entity not found"))
	}
	return connect.NewResponse(&entitystorev1.GetEntityResponse{Entity: e}), nil
}

func (m *mockEntityStore) GetRelationsFromEntity(_ context.Context, _ *connect.Request[entitystorev1.GetRelationsFromEntityRequest]) (*connect.Response[entitystorev1.GetRelationsFromEntityResponse], error) {
	return connect.NewResponse(&entitystorev1.GetRelationsFromEntityResponse{
		Relations: m.relations,
	}), nil
}

func (m *mockEntityStore) UpsertRelation(_ context.Context, req *connect.Request[entitystorev1.UpsertRelationRequest]) (*connect.Response[entitystorev1.UpsertRelationResponse], error) {
	m.upsertRelCalled++
	m.relations = append(m.relations, &entitystorev1.Relation{
		SourceId:     req.Msg.SourceId,
		TargetId:     req.Msg.TargetId,
		RelationType: req.Msg.RelationType,
	})
	return connect.NewResponse(&entitystorev1.UpsertRelationResponse{}), nil
}

func (m *mockEntityStore) FindByAnchors(_ context.Context, req *connect.Request[entitystorev1.FindByAnchorsRequest]) (*connect.Response[entitystorev1.FindByAnchorsResponse], error) {
	m.findAnchorCalled++
	var found []*entitystorev1.Entity
	for _, e := range m.entities {
		if e.EntityType != req.Msg.EntityType {
			continue
		}
		pb, err := unmarshalJobProto(e.Data)
		if err != nil {
			continue
		}
		for _, a := range req.Msg.Anchors {
			if a.Field == "workflow_id" && pb.WorkflowId == a.Value {
				found = append(found, e)
			}
		}
	}
	return connect.NewResponse(&entitystorev1.FindByAnchorsResponse{Entities: found}), nil
}

func (m *mockEntityStore) FindConnectedByType(_ context.Context, _ *connect.Request[entitystorev1.FindConnectedByTypeRequest]) (*connect.Response[entitystorev1.FindConnectedByTypeResponse], error) {
	var all []*entitystorev1.Entity
	for _, e := range m.entities {
		all = append(all, e)
	}
	return connect.NewResponse(&entitystorev1.FindConnectedByTypeResponse{Entities: all}), nil
}

func (m *mockEntityStore) GetEntitiesByType(_ context.Context, _ *connect.Request[entitystorev1.GetEntitiesByTypeRequest]) (*connect.Response[entitystorev1.GetEntitiesByTypeResponse], error) {
	var all []*entitystorev1.Entity
	for _, e := range m.entities {
		all = append(all, e)
	}
	return connect.NewResponse(&entitystorev1.GetEntitiesByTypeResponse{Entities: all}), nil
}

func (m *mockEntityStore) SetTags(_ context.Context, req *connect.Request[entitystorev1.SetTagsRequest]) (*connect.Response[entitystorev1.SetTagsResponse], error) {
	m.setTagsCalled++
	if e, ok := m.entities[req.Msg.EntityId]; ok {
		e.Tags = req.Msg.Tags
	}
	return connect.NewResponse(&entitystorev1.SetTagsResponse{}), nil
}

func (m *mockEntityStore) AddTags(_ context.Context, req *connect.Request[entitystorev1.AddTagsRequest]) (*connect.Response[entitystorev1.AddTagsResponse], error) {
	if e, ok := m.entities[req.Msg.EntityId]; ok {
		e.Tags = append(e.Tags, req.Msg.Tags...)
	}
	return connect.NewResponse(&entitystorev1.AddTagsResponse{}), nil
}

func (m *mockEntityStore) RemoveTag(_ context.Context, req *connect.Request[entitystorev1.RemoveTagRequest]) (*connect.Response[entitystorev1.RemoveTagResponse], error) {
	if e, ok := m.entities[req.Msg.EntityId]; ok {
		var filtered []string
		for _, t := range e.Tags {
			if t != req.Msg.Tag {
				filtered = append(filtered, t)
			}
		}
		e.Tags = filtered
	}
	return connect.NewResponse(&entitystorev1.RemoveTagResponse{}), nil
}

// --- Tests ---

func TestPublish_Success(t *testing.T) {
	store := newMockStore()
	client := NewClient(store)
	ctx := context.Background()

	job, err := client.Publish(ctx, PublishParams{
		WorkflowID: "wf-1",
		JobType:    "file_processing",
		OwnerID:    "user-1",
		TeamID:     "team-1",
		InputIDs:   []string{"doc-1", "doc-2"},
		Tags:       []string{"custom"},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if job.ID == "" {
		t.Error("expected non-empty job ID")
	}
	if job.Status != StatusPending {
		t.Errorf("status = %q, want %q", job.Status, StatusPending)
	}
	if job.WorkflowID != "wf-1" {
		t.Errorf("workflow_id = %q, want %q", job.WorkflowID, "wf-1")
	}
	if job.JobType != "file_processing" {
		t.Errorf("job_type = %q, want %q", job.JobType, "file_processing")
	}

	// 1 entity insert
	if store.insertCalled != 1 {
		t.Errorf("insertCalled = %d, want 1", store.insertCalled)
	}
	// 4 relations: owner + team + 2 inputs
	if store.upsertRelCalled != 4 {
		t.Errorf("upsertRelCalled = %d, want 4", store.upsertRelCalled)
	}
}

func TestPublish_MissingOwner(t *testing.T) {
	client := NewClient(newMockStore())
	_, err := client.Publish(context.Background(), PublishParams{
		JobType: "test",
	})
	if err == nil {
		t.Fatal("expected error for missing owner_id")
	}
	if !strings.Contains(err.Error(), "owner_id") {
		t.Errorf("error = %q, want mention of owner_id", err)
	}
}

func TestPublish_InputTooLarge(t *testing.T) {
	client := NewClient(newMockStore())
	bigInput := json.RawMessage(strings.Repeat("x", MaxInputSize+1))
	_, err := client.Publish(context.Background(), PublishParams{
		OwnerID: "user-1",
		Input:   bigInput,
	})
	if err == nil {
		t.Fatal("expected error for oversized input")
	}
	if !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("error = %q, want mention of maximum size", err)
	}
}

func TestPublish_MetadataTooLarge(t *testing.T) {
	client := NewClient(newMockStore())
	bigMeta := json.RawMessage(strings.Repeat("x", MaxMetadataSize+1))
	_, err := client.Publish(context.Background(), PublishParams{
		OwnerID:  "user-1",
		Metadata: bigMeta,
	})
	if err == nil {
		t.Fatal("expected error for oversized metadata")
	}
	if !strings.Contains(err.Error(), "maximum size") {
		t.Errorf("error = %q, want mention of maximum size", err)
	}
}

func TestFinalize_Success(t *testing.T) {
	store := newMockStore()
	client := NewClient(store)
	ctx := context.Background()

	job, err := client.Publish(ctx, PublishParams{
		WorkflowID: "wf-1",
		JobType:    "test",
		OwnerID:    "user-1",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	output := json.RawMessage(`{"result":"ok"}`)
	err = client.Finalize(ctx, job.ID, FinalizeParams{
		Status:    StatusCompleted,
		Output:    output,
		OutputIDs: []string{"out-1"},
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// Verify tags were updated.
	if store.setTagsCalled != 1 {
		t.Errorf("setTagsCalled = %d, want 1", store.setTagsCalled)
	}
}

func TestFinalize_AlreadyFinalized(t *testing.T) {
	store := newMockStore()
	client := NewClient(store)
	ctx := context.Background()

	job, err := client.Publish(ctx, PublishParams{
		WorkflowID: "wf-1",
		JobType:    "test",
		OwnerID:    "user-1",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// First finalize succeeds.
	err = client.Finalize(ctx, job.ID, FinalizeParams{Status: StatusCompleted})
	if err != nil {
		t.Fatalf("first Finalize: %v", err)
	}

	// Second finalize should fail with ErrAlreadyFinalized.
	err = client.Finalize(ctx, job.ID, FinalizeParams{Status: StatusFailed})
	if err == nil {
		t.Fatal("expected ErrAlreadyFinalized")
	}
	if !strings.Contains(err.Error(), "already finalized") {
		t.Errorf("error = %q, want mention of already finalized", err)
	}
}

func TestFinalize_InvalidStatus(t *testing.T) {
	client := NewClient(newMockStore())
	err := client.Finalize(context.Background(), "ent-1", FinalizeParams{
		Status: "banana",
	})
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
	if !strings.Contains(err.Error(), "invalid status") {
		t.Errorf("error = %q, want mention of invalid status", err)
	}
}

func TestFinalize_NonTerminalStatus(t *testing.T) {
	client := NewClient(newMockStore())
	err := client.Finalize(context.Background(), "ent-1", FinalizeParams{
		Status: StatusRunning,
	})
	if err == nil {
		t.Fatal("expected error for non-terminal status")
	}
	if !strings.Contains(err.Error(), "terminal status") {
		t.Errorf("error = %q, want mention of terminal status", err)
	}
}

func TestFinalize_OutputTooLarge(t *testing.T) {
	client := NewClient(newMockStore())
	big := json.RawMessage(strings.Repeat("x", MaxOutputSize+1))
	err := client.Finalize(context.Background(), "ent-1", FinalizeParams{
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

func TestGet_HydratesRelationships(t *testing.T) {
	store := newMockStore()
	client := NewClient(store)
	ctx := context.Background()

	job, err := client.Publish(ctx, PublishParams{
		WorkflowID: "wf-1",
		JobType:    "test",
		OwnerID:    "user-1",
		TeamID:     "team-1",
		InboxID:    "inbox-1",
		InputIDs:   []string{"doc-1"},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got, err := client.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Owner == nil || got.Owner.ID != "user-1" {
		t.Errorf("owner = %v, want user-1", got.Owner)
	}
	if got.Team == nil || got.Team.ID != "team-1" {
		t.Errorf("team = %v, want team-1", got.Team)
	}
	if got.Inbox == nil || got.Inbox.ID != "inbox-1" {
		t.Errorf("inbox = %v, want inbox-1", got.Inbox)
	}
	if len(got.Inputs) != 1 || got.Inputs[0].ID != "doc-1" {
		t.Errorf("inputs = %v, want [doc-1]", got.Inputs)
	}
}

func TestGetByWorkflowID_Success(t *testing.T) {
	store := newMockStore()
	client := NewClient(store)
	ctx := context.Background()

	published, err := client.Publish(ctx, PublishParams{
		WorkflowID: "wf-42",
		JobType:    "test",
		OwnerID:    "user-1",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got, err := client.GetByWorkflowID(ctx, "wf-42")
	if err != nil {
		t.Fatalf("GetByWorkflowID: %v", err)
	}

	if got.ID != published.ID {
		t.Errorf("ID = %q, want %q", got.ID, published.ID)
	}
	if got.WorkflowID != "wf-42" {
		t.Errorf("workflow_id = %q, want wf-42", got.WorkflowID)
	}
}

func TestGetByWorkflowID_NotFound(t *testing.T) {
	client := NewClient(newMockStore())
	_, err := client.GetByWorkflowID(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected ErrNotFound")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want mention of not found", err)
	}
}

func TestCancel(t *testing.T) {
	store := newMockStore()
	client := NewClient(store)
	ctx := context.Background()

	job, err := client.Publish(ctx, PublishParams{
		WorkflowID: "wf-1",
		JobType:    "test",
		OwnerID:    "user-1",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	err = client.Cancel(ctx, job.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got, err := client.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get after cancel: %v", err)
	}
	if got.Status != StatusCancelled {
		t.Errorf("status = %q, want %q", got.Status, StatusCancelled)
	}
}

func TestReportProgress(t *testing.T) {
	store := newMockStore()
	client := NewClient(store)
	ctx := context.Background()

	job, err := client.Publish(ctx, PublishParams{
		WorkflowID: "wf-1",
		JobType:    "test",
		OwnerID:    "user-1",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
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

	got, err := client.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
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

func TestList_WithFilters(t *testing.T) {
	store := newMockStore()
	client := NewClient(store)
	ctx := context.Background()

	// Create two jobs with different types.
	for _, jt := range []string{"type_a", "type_b"} {
		_, err := client.Publish(ctx, PublishParams{
			WorkflowID: "wf-" + jt,
			JobType:    jt,
			OwnerID:    "user-1",
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	// List all.
	all, err := client.List(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("len = %d, want 2", len(all))
	}

	// Filter by job type.
	filtered, err := client.List(ctx, ListFilter{JobType: "type_a"})
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("len = %d, want 1", len(filtered))
	}
}

func TestList_Pagination(t *testing.T) {
	store := newMockStore()
	client := NewClient(store)
	ctx := context.Background()

	for i := range 5 {
		_, err := client.Publish(ctx, PublishParams{
			WorkflowID: fmt.Sprintf("wf-%d", i),
			JobType:    "test",
			OwnerID:    "user-1",
		})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	page, err := client.List(ctx, ListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page) != 2 {
		t.Errorf("len = %d, want 2", len(page))
	}
}

func TestList_InvalidStatus(t *testing.T) {
	client := NewClient(newMockStore())
	_, err := client.List(context.Background(), ListFilter{Status: "banana"})
	if err == nil {
		t.Fatal("expected error for invalid status filter")
	}
}

func TestRebuildTags(t *testing.T) {
	tags := rebuildTags([]string{"pending", "custom", "env:prod"}, StatusCompleted)

	hasCustom, hasEnv, hasCompleted, hasPending := false, false, false, false
	for _, t := range tags {
		switch t {
		case "custom":
			hasCustom = true
		case "env:prod":
			hasEnv = true
		case StatusCompleted:
			hasCompleted = true
		case StatusPending:
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
	for _, s := range []string{StatusPending, StatusRunning, StatusCompleted, StatusFailed, StatusCancelled} {
		if err := validateStatus(s); err != nil {
			t.Errorf("validateStatus(%q) = %v, want nil", s, err)
		}
	}
	if err := validateStatus("banana"); err == nil {
		t.Error("validateStatus(banana) = nil, want error")
	}
}
