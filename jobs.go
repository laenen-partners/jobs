// Package jobs provides a store-agnostic SDK for durable job tracking.
//
// The core package contains only pure Go types (Job, Step, Progress, Status)
// and the Store interface. It has zero dependencies on protobuf or entitystore.
//
// Persistence is pluggable via the JobStore interface. See the postgres
// subpackage for a Postgres implementation, or implement your own.
//
// The Client handles orchestration (Run/Step lifecycle, retries, validation)
// and delegates all persistence to the injected JobStore.
package jobs

import (
	"encoding/json"
	"fmt"
	"time"
)

// Entity is the pubsub entity name used in change notifications.
// Use this when subscribing to job changes via stream scopes
// (e.g. "jobs:*" or "jobs:<id>").
const Entity = "jobs"

// Size limits for data fields.
const (
	MaxInputSize      = 1 << 20   // 1 MiB
	MaxOutputSize     = 1 << 20   // 1 MiB
	MaxMetadataSize   = 64 << 10  // 64 KiB
	MaxStepInputSize  = 256 << 10 // 256 KiB
	MaxStepOutputSize = 256 << 10 // 256 KiB
)

// Status represents a job lifecycle state.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// AllStatuses lists every valid status value.
var AllStatuses = []Status{
	StatusPending, StatusRunning, StatusCompleted, StatusFailed, StatusCancelled,
}

// TerminalStatuses are statuses that cannot transition to another state.
var TerminalStatuses = map[Status]bool{
	StatusCompleted: true,
	StatusFailed:    true,
	StatusCancelled: true,
}

// DefaultListLimit is the default maximum number of jobs returned by List.
const DefaultListLimit = 100

// Job represents a tracked unit of work.
type Job struct {
	ID                string          `json:"id"`
	ExternalReference string          `json:"external_reference"`
	JobType           string          `json:"job_type"`
	Status            Status          `json:"status"`
	Progress          *Progress       `json:"progress,omitempty"`
	Error             string          `json:"error,omitempty"`
	Input             json.RawMessage `json:"input,omitempty"`
	Output            json.RawMessage `json:"output,omitempty"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
	Tags              []string        `json:"tags,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// Progress tracks in-flight job progress.
type Progress struct {
	Step      string    `json:"step"`
	Current   int       `json:"current"`
	Total     int       `json:"total"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Step is the hydrated view of a job step.
type Step struct {
	ID          string          `json:"id"`
	JobID       string          `json:"job_id"`
	Name        string          `json:"name"`
	Sequence    int             `json:"sequence"`
	Status      Status          `json:"status"`
	Error       string          `json:"error,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
	Output      json.RawMessage `json:"output,omitempty"`
	StartedAt   time.Time       `json:"started_at,omitzero"`
	CompletedAt time.Time       `json:"completed_at,omitzero"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// RegisterJobParams are the inputs for creating a new job.
type RegisterJobParams struct {
	ExternalReference string          `json:"external_reference"`
	JobType           string          `json:"job_type"`
	Tags              []string        `json:"tags,omitempty"`
	Input             json.RawMessage `json:"input,omitempty"`    // primary input payload (max 1 MiB)
	Metadata          json.RawMessage `json:"metadata,omitempty"` // ancillary caller metadata (max 64 KiB)
}

// FinalizeParams describe the final state of a completed/failed/cancelled job.
type FinalizeParams struct {
	Status Status          `json:"status"` // completed, failed, cancelled
	Error  string          `json:"error,omitempty"`
	Output json.RawMessage `json:"output,omitempty"` // primary output payload (max 1 MiB)
	Tags   []string        `json:"tags,omitempty"`   // additional tags to add on finalize
}

// RegisterStepParams are the inputs for creating a new step within a job.
type RegisterStepParams struct {
	Name     string          `json:"name"`
	Sequence int             `json:"sequence"`
	Input    json.RawMessage `json:"input,omitempty"` // max 256 KiB
}

// CompleteStepParams describe the outcome of a finished step.
type CompleteStepParams struct {
	Output json.RawMessage `json:"output,omitempty"` // max 256 KiB
}

// SortField identifies a column that jobs can be sorted by.
type SortField string

const (
	SortByCreatedAt SortField = "created_at"
	SortByUpdatedAt SortField = "updated_at"
	SortByJobType   SortField = "job_type"
	SortByStatus    SortField = "status"
)

// allowedSortFields is the allowlist of columns that can be used for sorting.
var allowedSortFields = map[SortField]bool{
	SortByCreatedAt: true,
	SortByUpdatedAt: true,
	SortByJobType:   true,
	SortByStatus:    true,
}

// SortDirection controls ascending vs descending sort order.
type SortDirection string

const (
	SortAsc  SortDirection = "asc"
	SortDesc SortDirection = "desc"
)

// ListFilter controls which jobs are returned by List.
// All tags are AND-combined — a job must have every tag to match.
type ListFilter struct {
	Tags    []string      // all must match
	Limit   int           // max results (0 = DefaultListLimit)
	Offset  int           // skip first N results
	SortBy  SortField     // column to sort by (default: created_at)
	SortDir SortDirection // sort direction (default: desc)
}

// ValidateSort checks that a sort field and direction are valid.
func ValidateSort(field SortField, dir SortDirection) error {
	if field != "" && !allowedSortFields[field] {
		return fmt.Errorf("jobs: invalid sort field %q: must be one of created_at, updated_at, job_type, status", field)
	}
	if dir != "" && dir != SortAsc && dir != SortDesc {
		return fmt.Errorf("jobs: invalid sort direction %q: must be asc or desc", dir)
	}
	return nil
}

// ValidateStatus checks that a status value is a known lifecycle state.
func ValidateStatus(s Status) error {
	for _, valid := range AllStatuses {
		if s == valid {
			return nil
		}
	}
	return fmt.Errorf("jobs: invalid status %q: must be one of pending, running, completed, failed, cancelled", s)
}

// RebuildTags replaces any status tag in the existing tags with the new status.
func RebuildTags(existingTags []string, newStatus Status) []string {
	statusSet := make(map[Status]bool, len(AllStatuses))
	for _, s := range AllStatuses {
		statusSet[s] = true
	}
	tags := make([]string, 0, len(existingTags)+1)
	for _, t := range existingTags {
		if !statusSet[Status(t)] {
			tags = append(tags, t)
		}
	}
	return append(tags, string(newStatus))
}

// HasAllTags returns true if entityTags contains every tag in requiredTags.
func HasAllTags(entityTags, requiredTags []string) bool {
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

// ValidateData checks that a data payload does not exceed maxSize bytes.
func ValidateData(data json.RawMessage, maxSize int, name string) error {
	if len(data) > maxSize {
		return fmt.Errorf("jobs: %s exceeds maximum size of %d bytes", name, maxSize)
	}
	return nil
}
