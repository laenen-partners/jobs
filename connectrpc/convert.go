package connectrpc

import (
	"errors"

	connect "connectrpc.com/connect"

	"github.com/laenen-partners/jobs"
	jobsv1 "github.com/laenen-partners/jobs/connectrpc/gen/jobs/v1"
)

// --- Domain → Proto ---

func jobToProto(j *jobs.Job) *jobsv1.Job {
	pj := &jobsv1.Job{
		Id:                j.ID,
		ExternalReference: j.ExternalReference,
		JobType:           j.JobType,
		Status:            statusToProto(j.Status),
		Error:             j.Error,
		Input:             j.Input,
		Output:            j.Output,
		Metadata:          j.Metadata,
		Tags:              j.Tags,
		CreatedAt:         j.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:         j.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if j.Progress != nil {
		pj.Progress = &jobsv1.Progress{
			Step:      j.Progress.Step,
			Current:   int32(j.Progress.Current),
			Total:     int32(j.Progress.Total),
			Message:   j.Progress.Message,
			UpdatedAt: j.Progress.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}
	return pj
}

func stepToProto(s *jobs.Step) *jobsv1.Step {
	ps := &jobsv1.Step{
		Id:       s.ID,
		JobId:    s.JobID,
		Name:     s.Name,
		Sequence: int32(s.Sequence),
		Status:   statusToProto(s.Status),
		Error:    s.Error,
	}
	if !s.StartedAt.IsZero() {
		ps.StartedAt = s.StartedAt.Format("2006-01-02T15:04:05Z")
	}
	if !s.CompletedAt.IsZero() {
		ps.CompletedAt = s.CompletedAt.Format("2006-01-02T15:04:05Z")
	}
	return ps
}

func stepsToProto(ss []jobs.Step) []*jobsv1.Step {
	out := make([]*jobsv1.Step, len(ss))
	for i := range ss {
		out[i] = stepToProto(&ss[i])
	}
	return out
}

func statusToProto(s jobs.Status) jobsv1.Status {
	switch s {
	case jobs.StatusPending:
		return jobsv1.Status_STATUS_PENDING
	case jobs.StatusRunning:
		return jobsv1.Status_STATUS_RUNNING
	case jobs.StatusCompleted:
		return jobsv1.Status_STATUS_COMPLETED
	case jobs.StatusFailed:
		return jobsv1.Status_STATUS_FAILED
	case jobs.StatusCancelled:
		return jobsv1.Status_STATUS_CANCELLED
	default:
		return jobsv1.Status_STATUS_UNSPECIFIED
	}
}

// --- Proto → Domain ---

func statusFromProto(s jobsv1.Status) jobs.Status {
	switch s {
	case jobsv1.Status_STATUS_PENDING:
		return jobs.StatusPending
	case jobsv1.Status_STATUS_RUNNING:
		return jobs.StatusRunning
	case jobsv1.Status_STATUS_COMPLETED:
		return jobs.StatusCompleted
	case jobsv1.Status_STATUS_FAILED:
		return jobs.StatusFailed
	case jobsv1.Status_STATUS_CANCELLED:
		return jobs.StatusCancelled
	default:
		return ""
	}
}

// --- Error mapping ---

func toConnectError(err error) *connect.Error {
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, jobs.ErrAlreadyFinalized):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, jobs.ErrNoStore):
		return connect.NewError(connect.CodeInternal, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
