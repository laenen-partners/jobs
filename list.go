package jobs

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	entitystorev1 "github.com/laenen-partners/entitystore/gen/entitystore/v1"
)

// DefaultListLimit is the default maximum number of jobs returned by List.
const DefaultListLimit = 100

// ListFilter controls which jobs are returned by List.
// Filters are AND-combined: setting OwnerID and Status returns jobs
// matching both criteria.
type ListFilter struct {
	OwnerID string // jobs owned_by this entity
	TeamID  string // jobs assigned_to this entity
	InboxID string // jobs created_from this entity
	Status  string // maps to tag filter (pending, completed, failed, ...)
	JobType string // filter by job_type in entity data
	Tags    []string
	Limit   int // max results (0 = DefaultListLimit)
	Offset  int // skip first N results
}

// List queries jobs from EntityStore using relationship and tag filters.
//
// When a relationship filter is set (OwnerID, TeamID, InboxID), List uses
// FindConnectedByType to traverse the graph. Otherwise it falls back to
// GetEntitiesByType with tag filtering.
func (c *Client) List(ctx context.Context, filter ListFilter) ([]Job, error) {
	if filter.Status != "" {
		if err := validateStatus(filter.Status); err != nil {
			return nil, err
		}
	}

	// Build tag filter.
	tags := append([]string{}, filter.Tags...)
	if filter.Status != "" {
		tags = append(tags, filter.Status)
	}

	var queryFilter *entitystorev1.QueryFilter
	if len(tags) > 0 {
		queryFilter = &entitystorev1.QueryFilter{Tags: tags}
	}

	// Determine query strategy based on filters.
	var entities []*entitystorev1.Entity
	var err error

	switch {
	case filter.OwnerID != "":
		entities, err = c.findConnected(ctx, filter.OwnerID, []string{RelOwnedBy}, queryFilter)
	case filter.TeamID != "":
		entities, err = c.findConnected(ctx, filter.TeamID, []string{RelAssignedTo}, queryFilter)
	case filter.InboxID != "":
		entities, err = c.findConnected(ctx, filter.InboxID, []string{RelCreatedFrom}, queryFilter)
	default:
		entities, err = c.findByType(ctx, queryFilter)
	}
	if err != nil {
		return nil, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}

	// Convert to Job structs with pagination.
	jobs := make([]Job, 0, min(len(entities), limit))
	skipped := 0
	for _, e := range entities {
		job, err := entityToJob(e)
		if err != nil {
			continue // skip malformed entities
		}

		// Client-side filter for job_type (stored in entity data, not a tag).
		if filter.JobType != "" && job.JobType != filter.JobType {
			continue
		}

		// Apply offset.
		if skipped < filter.Offset {
			skipped++
			continue
		}

		jobs = append(jobs, *job)
		if len(jobs) >= limit {
			break
		}
	}

	return jobs, nil
}

func (c *Client) findConnected(ctx context.Context, entityID string, relationTypes []string, filter *entitystorev1.QueryFilter) ([]*entitystorev1.Entity, error) {
	resp, err := c.entities.FindConnectedByType(ctx, connect.NewRequest(&entitystorev1.FindConnectedByTypeRequest{
		EntityId:      entityID,
		EntityType:    EntityType,
		RelationTypes: relationTypes,
		Filter:        filter,
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs: find connected: %w", err)
	}
	return resp.Msg.Entities, nil
}

func (c *Client) findByType(ctx context.Context, filter *entitystorev1.QueryFilter) ([]*entitystorev1.Entity, error) {
	resp, err := c.entities.GetEntitiesByType(ctx, connect.NewRequest(&entitystorev1.GetEntitiesByTypeRequest{
		EntityType: EntityType,
	}))
	if err != nil {
		return nil, fmt.Errorf("jobs: get by type: %w", err)
	}

	if filter == nil || len(filter.Tags) == 0 {
		return resp.Msg.Entities, nil
	}

	// Client-side tag filter.
	var filtered []*entitystorev1.Entity
	for _, e := range resp.Msg.Entities {
		if hasAllTags(e.Tags, filter.Tags) {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

func hasAllTags(entityTags, requiredTags []string) bool {
	tagSet := make(map[string]struct{}, len(entityTags))
	for _, t := range entityTags {
		tagSet[t] = struct{}{}
	}
	for _, t := range requiredTags {
		if _, ok := tagSet[t]; !ok {
			return false
		}
	}
	return true
}
